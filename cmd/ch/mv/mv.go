package mv

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"time"

	"github.com/coyove/iis/cmd/ch/config"
)

var ErrNotExisted = errors.New("article not existed")

type Cmd string

const (
	CmdNone     Cmd = ""
	CmdReply        = "inbox-reply"
	CmdMention      = "inbox-mention"
	CmdFollow       = "follow"
	CmdFollowed     = "followed"
	CmdBlock        = "block"
	CmdLike         = "like"
	CmdVote         = "vote"

	DeletionMarker = "[[b19b8759-391b-460a-beb0-16f5f334c34f]]"
)

type Article struct {
	ID          string            `json:"id"`
	Replies     int               `json:"rs,omitempty"`
	Likes       int32             `json:"like,omitempty"`
	Locked      bool              `json:"lock,omitempty"`
	NSFW        bool              `json:"nsfw,omitempty"`
	Content     string            `json:"content,omitempty"`
	Media       string            `json:"M,omitempty"`
	Author      string            `json:"author,omitempty"`
	IP          string            `json:"ip,omitempty"`
	CreateTime  time.Time         `json:"create,omitempty"`
	Parent      string            `json:"P,omitempty"`
	ReplyChain  string            `json:"Rc,omitempty"`
	NextReplyID string            `json:"R,omitempty"`
	NextMediaID string            `json:"MN,omitempty"`
	NextID      string            `json:"N,omitempty"`
	EOC         string            `json:"EO,omitempty"`
	Cmd         Cmd               `json:"K,omitempty"`
	Extras      map[string]string `json:"X,omitempty"`
	ReferID     string            `json:"ref,omitempty"`
}

func (a *Article) ContentHTML() template.HTML {
	if a.Content == DeletionMarker {
		a.Extras = nil
		return "<span class=deleted></span>"
	}
	return template.HTML(sanText(a.Content))
}

func (a *Article) PickNextID(media bool) string {
	if media {
		return a.NextMediaID
	}
	return a.NextID
}

func (a *Article) Marshal() []byte {
	b, _ := json.Marshal(a)
	return b
}

func UnmarshalArticle(b []byte) (*Article, error) {
	a := &Article{}
	err := json.Unmarshal(b, a)
	if a.ID == "" {
		return nil, fmt.Errorf("failed to unmarshal: %q", b)
	}
	return a, err
}

type User struct {
	ID             string
	Session        string
	Role           string
	PasswordHash   []byte
	Email          string `json:"e"`
	Avatar         int    `json:"av"`
	CustomName     string `json:"cn"`
	Followers      int32  `json:"F"`
	Followings     int32  `json:"f"`
	Unread         int32  `json:"ur"`
	FollowingChain string `json:"FC2,omitempty"`
	DataIP         string `json:"sip"`
	TSignup        uint32 `json:"st"`
	TLogin         uint32 `json:"lt"`
	Banned         bool   `json:"ban,omitempty"`
	Kimochi        byte   `json:"kmc,omitempty"`

	_IsFollowing bool
	_IsBlocking  bool
	_IsNotYou    bool
	_Settings    UserSettings
}

func (u User) Marshal() []byte {
	b, _ := json.Marshal(u)
	return b
}

func (u User) DisplayName() string {
	if u.CustomName == "" {
		return "@" + u.ID
	}
	return u.CustomName + " (@" + u.ID + ")"
}

func (u User) IsFollowing() bool { return u._IsFollowing }

func (u User) IsBlocking() bool { return u._IsBlocking }

func (u User) IsNotYou() bool { return u._IsNotYou }

func (u User) Settings() UserSettings { return u._Settings }

func (u *User) SetIsFollowing(v bool) { u._IsFollowing = v }

func (u *User) SetIsBlocking(v bool) { u._IsBlocking = v }

func (u *User) SetIsNotYou(v bool) { u._IsNotYou = v }

func (u *User) SetSettings(s UserSettings) { u._Settings = s }

func (u User) JSON() string {
	b, _ := json.MarshalIndent(u, "", "")
	b = bytes.TrimLeft(b, " \r\n\t{")
	b = bytes.TrimRight(b, " \r\n\t}")
	return string(b)
}

func (u User) Signup() time.Time { return time.Unix(int64(u.TSignup), 0) }

func (u User) Login() time.Time { return time.Unix(int64(u.TLogin), 0) }

func (u User) IsMod() bool { return u.Role == "mod" || u.ID == config.Cfg.AdminName }

func (u User) IsAdmin() bool { return u.Role == "admin" || u.ID == config.Cfg.AdminName }

func (u User) IDHash() (hash uint64) {
	for _, r := range u.ID {
		hash = hash*31 + uint64(r)
	}
	return
}

func UnmarshalUser(b []byte) (*User, error) {
	a := &User{}
	err := json.Unmarshal(b, a)
	if a.ID == "" {
		return nil, fmt.Errorf("failed to unmarshal: %q", b)
	}

	AddUserToSearch(a.ID)
	return a, err
}

type UserSettings struct {
	AutoNSFW    bool   `json:"autonsfw,omitempty"`
	FoldImages  bool   `json:"foldi,omitempty"`
	Description string `json:"desc,omitempty"`
}

func (u UserSettings) Marshal() []byte {
	p, _ := json.Marshal(u)
	return p
}

func (u UserSettings) DescHTML() template.HTML {
	return template.HTML(sanText(u.Description))
}

// Always return a valid struct, though sometimes being empty
func UnmarshalUserSettings(b []byte) UserSettings {
	a := UserSettings{}
	json.Unmarshal(b, &a)
	return a
}

func MakeUserToken(u *User) string {
	if u == nil {
		return ""
	}

	length := len(u.ID) + 1 + len(u.Session)
	length = (length + 7) / 8 * 8

	x := make([]byte, length)
	copy(x, u.Session)
	copy(x[len(u.Session)+1:], u.ID)

	for i := 0; i <= len(x)-16; i += 8 {
		config.Cfg.Blk.Encrypt(x[i:], x[i:])
	}
	return base64.StdEncoding.EncodeToString(x)
}
