package dal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/coyove/iis/common"
	"github.com/coyove/iis/ik"
	"github.com/coyove/iis/model"
	"github.com/gin-gonic/gin"
)

func GetUser(id string) (*model.User, error) {
	if id == "" {
		return nil, fmt.Errorf("empty user id")
	}
	if u := m.weakUsers.Get(id); u != nil {
		return (*model.User)(u), nil
	}

	p, err := m.db.Get("u/" + id)
	if err != nil {
		return nil, err
	}

	if len(p) == 0 {
		return nil, model.ErrNotExisted
	}

	u, err := model.UnmarshalUser(p)
	if u != nil {
		u2 := *u
		m.weakUsers.Add(u.ID, unsafe.Pointer(&u2))
		if u.FollowingChain != "" {
			go func() {
				fc, _ := GetArticle(u.FollowingChain)
				id := ik.NewID(ik.IDFollowing, u.ID).String()
				if fc != nil {
					for !strings.HasPrefix(fc.NextID, "u/") {
						fc2, _ := GetArticle(fc.NextID)
						if fc2 != nil {
							fc = fc2
						} else {
							log.Println(u.ID, "following chain broken")
							break
						}
					}
					a := &model.Article{
						ID:     id,
						NextID: fc.NextID,
					}
					if _, err := GetArticle(a.ID); err == model.ErrNotExisted {
						m.db.Set(a.ID, a.Marshal())
					}
				}
			}()
		}
	}

	return u, err
}

func GetUserWithSettings(id string) (*model.User, error) {
	u, err := GetUser(id)
	if err != nil {
		return u, err
	}
	p, _ := m.db.Get("u/" + id + "/settings")
	u.SetSettings(model.UnmarshalUserSettings(p))
	return u, nil
}

func GetUserByContext(g *gin.Context) *model.User {
	u, _ := GetUserByToken(g.PostForm("api2_uid"))
	if u != nil && u.Banned {
		return nil
	}
	return u
}

func GetUserByToken(tok string) (*model.User, error) {
	if tok == "" {
		return nil, fmt.Errorf("invalid token")
	}

	x, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return nil, err
	}

	for i := len(x) - 16; i >= 0; i -= 8 {
		common.Cfg.Blk.Decrypt(x[i:], x[i:])
	}

	parts := bytes.SplitN(x, []byte("\x00"), 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid token format")
	}

	session, id := parts[0], parts[1]
	u, err := GetUserWithSettings(string(id))
	if err != nil {
		return nil, err
	}

	if u.Session != string(session) {
		return nil, fmt.Errorf("invalid token session")
	}
	return u, nil
}

func MentionUserAndTags(a *model.Article, ids []string, tags []string) error {
	for _, id := range ids {
		if IsBlocking(id, a.Author) {
			return fmt.Errorf("author blocked")
		}

		if err := Do(NewRequest(DoInsertArticle,
			"RootID", ik.NewID(ik.IDInbox, id).String(),
			"Article", model.Article{
				ID:  ik.NewGeneralID().String(),
				Cmd: model.CmdMention,
				Extras: map[string]string{
					"from":       a.Author,
					"article_id": a.ID,
				},
				CreateTime: time.Now(),
			},
		)); err != nil {
			return err
		}
		if err := Do(NewRequest(DoUpdateUser, "ID", id, "IncUnread", true)); err != nil {
			return err
		}
	}

	for _, tag := range tags {
		if err := Do(NewRequest(DoInsertArticle,
			"RootID", ik.NewID(ik.IDTag, tag).String(),
			"Article", model.Article{
				ID:         ik.NewGeneralID().String(),
				ReferID:    a.ID,
				Media:      a.Media,
				CreateTime: time.Now(),
			},
		)); err != nil {
			return err
		}
		common.AddTagToSearch(tag)
	}
	return nil
}

func FollowUser(from, to string, following bool) (E error) {
	followID := makeFollowID(from, to)
	if following && IsBlocking(to, from) {
		// "from" wants "to" follow "to" but "to" blocked "from"
		return fmt.Errorf("follow/to-blocked")
	}

	updated := false
	defer func() {
		if E != nil || !updated {
			return
		}

		go func() {
			Do(NewRequest(DoUpdateUser, "ID", from, "IncDecFollowings", following))
			if !strings.HasPrefix(to, "#") {
				fromFollowToNotifyTo(from, to, following)
			}
		}()
	}()

	r := NewRequest(DoUpdateArticle,
		"ID", followID,
		"SetExtraKey", to,
		"SetExtraValue", strconv.FormatBool(following)+","+strconv.FormatInt(time.Now().Unix(), 10),
	)
	if err := Do(r); err != nil {
		if err == model.ErrNotExisted {
			updated = true
			if err := Do(NewRequest(DoInsertArticle,
				"RootID", ik.NewID(ik.IDFollowing, from).String(),
				"Article", model.Article{
					ID:         followID,
					Cmd:        model.CmdFollow,
					Extras:     map[string]string{to: *r.UpdateArticleRequest.SetExtraValue},
					CreateTime: time.Now(),
				},
			)); err != nil {
				return err
			}
			return nil
		}
		return err
	}
	if !strings.HasPrefix(r.UpdateArticleRequest.Response.OldExtraValue, strconv.FormatBool(following)) {
		updated = true
	}
	return nil
}

func fromFollowToNotifyTo(from, to string, following bool) (E error) {
	if err := Do(NewRequest(DoUpdateUser, "ID", to, "IncDecFollowers", following)); err != nil {
		return err
	}
	_, err := insertChainOrUpdate(
		makeFollowedID(to, from),
		ik.NewID(ik.IDFollower, to).String(),
		from,
		model.CmdFollowed,
		following)
	return err
}

func BlockUser(from, to string, blocking bool) (E error) {
	if blocking {
		if err := FollowUser(to, from, false); err != nil {
			log.Println("Block user:", to, "unfollow error:", err)
		}
	}

	_, err := insertChainOrUpdate(
		makeBlockID(from, to),
		ik.NewID(ik.IDBlacklist, from).String(),
		to,
		model.CmdBlock,
		blocking)
	return err
}

func LikeArticle(from, to string, liking bool) (E error) {
	updated, err := insertChainOrUpdate(
		makeLikeID(from, to),
		ik.NewID(ik.IDLike, from).String(),
		to,
		model.CmdLike,
		liking)
	if err != nil {
		return err
	}
	if updated {
		go func() {
			r := NewRequest(DoUpdateArticle, "ID", to, "IncDecLikes", liking)
			if err := Do(r); err == nil {
				// if the author followed 'from', notify the author that his articles has been liked by 'from'
				if a := r.UpdateArticleRequest.Response.Article; IsFollowing(a.Author, from) && liking {
					Do(NewRequest(DoInsertArticle,
						"RootID", ik.NewID(ik.IDInbox, a.Author).String(),
						"Article", model.Article{
							ID:  ik.NewGeneralID().String(),
							Cmd: model.CmdILike,
							Extras: map[string]string{
								"from":       from,
								"article_id": a.ID,
							},
							CreateTime: time.Now(),
						}))
					Do(NewRequest(DoUpdateUser, "ID", a.Author, "IncUnread", true))
				}
			}

		}()
	}
	return nil
}

func insertChainOrUpdate(aid, chainid string, to string, cmd model.Cmd, value bool) (updated bool, E error) {
	state := strconv.FormatBool(value)
	r := NewRequest(DoUpdateArticle, "ID", aid, "SetExtraKey", string(cmd), "SetExtraValue", state)
	if err := Do(r); err != nil {
		if err == model.ErrNotExisted {
			a := &model.Article{
				ID:  aid,
				Cmd: cmd,
				Extras: map[string]string{
					"to":        to,
					string(cmd): strconv.FormatBool(value),
				},
				CreateTime: time.Now(),
			}

			if cmd == model.CmdLike {
				toa, _ := GetArticle(to)
				if toa != nil {
					a.Media = toa.Media
				}
			}

			return true, Do(NewRequest(DoInsertArticle, "RootID", chainid, "Article", *a))
		}
		return false, err
	}
	return r.UpdateArticleRequest.Response.OldExtraValue != state, nil
}

type FollowingState struct {
	ID          string
	Time        time.Time
	Followed    bool
	RevFollowed bool
	Liked       bool
	Blocked     bool
}

func GetFollowingList(chain ik.ID, cursor string, n int) ([]FollowingState, string) {
	if cursor == "" {
		a, err := GetArticle(chain.String())
		if err != nil {
			if err != model.ErrNotExisted {
				log.Println("[GetFollowingList] Failed to get chain [", chain, "]")
			}
			return nil, ""
		}
		cursor = a.NextID
	}

	res := []FollowingState{}
	start := time.Now()

	for len(res) < n && strings.HasPrefix(cursor, "u/") {
		if time.Since(start).Seconds() > 0.2 {
			log.Println("[GetFollowingList] Break out slow walk [", cursor, "]")
			break
		}

		a, err := GetArticle(cursor)
		if err != nil {
			log.Println("[GetFollowingList]", cursor, err)
			break
		}

		if a.Cmd == model.CmdFollow {
			for k, v := range a.Extras {
				p := strings.Split(v, ",")
				if len(p) != 2 {
					continue
				}
				res = append(res, FollowingState{
					ID:       k,
					Time:     time.Unix(atoi64(p[1]), 0),
					Followed: atob(p[0]),
				})
			}
		} else {
			res = append(res, FollowingState{
				ID:          a.Extras["to"],
				Time:        a.CreateTime,
				Blocked:     a.Extras["block"] == "true",
				RevFollowed: a.Extras["followed"] == "true",
				Liked:       a.Extras["like"] == "true",
			})
		}

		cursor = a.NextID
	}

	sort.Slice(res, func(i, j int) bool { return res[i].Time.After(res[j].Time) })

	return res, cursor
}

func IsFollowing(from, to string) bool {
	p, _ := GetArticle(makeFollowID(from, to))
	return p != nil && strings.HasPrefix(p.Extras[to], "true")
}

func IsBlocking(from, to string) bool {
	p, _ := GetArticle(makeBlockID(from, to))
	return p != nil && p.Extras["block"] == "true"
}

func IsLiking(from, to string) bool {
	p, _ := GetArticle(makeLikeID(from, to))
	return p != nil && p.Extras["like"] == "true"
}
