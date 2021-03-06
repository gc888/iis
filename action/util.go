package action

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/coyove/iis/common"
	"github.com/coyove/iis/ik"
	"github.com/coyove/iis/model"
	"github.com/gin-gonic/gin"
)

func checkIP(g *gin.Context) string {
	if u, _ := g.Get("user"); u != nil {
		if u.(*model.User).IsMod() {
			return ""
		}
	}
	if !g.GetBool("ip-ok") {
		return fmt.Sprintf("guard/cooling-down/%.1fs", float64(common.Cfg.Cooldown)-g.GetFloat64("ip-ok-remain"))
	}
	return ""
}

func checkToken(g *gin.Context) string {
	var (
		uuid       = common.SoftTrunc(g.PostForm("uuid"), 32)
		_, tokenok = ik.ParseToken(g, uuid)
	)

	if ret := checkIP(g); ret != "" {
		return ret
	}

	if u, ok := g.Get("user"); ok {
		if u.(*model.User).Banned {
			return "guard/id-not-existed"
		}
	}

	// Admin still needs token verification
	if !tokenok {
		return "guard/token-expired"
	}

	return ""
}

func sanUsername(id string) string {
	return common.SafeStringForCompressString(id)
}

func checkCaptcha(g *gin.Context) string {
	var (
		answer            = common.SoftTrunc(g.PostForm("answer"), 6)
		uuid              = common.SoftTrunc(g.PostForm("uuid"), 32)
		tokenbuf, tokenok = ik.ParseToken(g, uuid)
		challengePassed   bool
	)

	if ret := checkIP(g); ret != "" {
		return ret
	}

	if !tokenok {
		return "guard/token-expired"
	}

	if len(answer) == 4 {
		challengePassed = true
		for i := range answer {
			a := answer[i]
			if a >= 'A' && a <= 'Z' {
				a = a - 'A' + 'a'
			}

			if a != "0123456789acefhijklmnpqrtuvwxyz"[tokenbuf[i]%31] &&
				a != "oiz3asg7b9acefhijklmnpqrtuvwxyz"[tokenbuf[i]%31] {
				challengePassed = false
				break
			}
		}
	}

	if !challengePassed {
		log.Println(g.MustGet("ip"), "challenge failed")
		return "guard/failed-captcha"
	}

	if !tokenok {
		return "guard/token-expired"
	}

	return ""
}

func genSession() string {
	p := [12]byte{}
	rand.Read(p[:])
	for i := range p {
		if p[i] == 0 {
			p[i] = 1
		}
	}
	return base64.URLEncoding.EncodeToString(p[:])
}

func writeImage(u *model.User, imageName, image string) (string, error) {
	image = image[strings.Index(image, ",")+1:]
	dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(image))

	hash := uint64(0)
	for i := len(image) - 1; i >= 0 && i >= len(image)-1024; i-- {
		hash = hash*31 + uint64(image[i])
	}
	hash = hash&0xfffffffffffff000 | (uint64(len(image)/4*3/1024) & 0xfff)

	path := fmt.Sprintf("tmp/images/%d/", hash%1024)
	fn := fmt.Sprintf("%016x", hash)

	if imageName != "" {
		imageName = filepath.Base(imageName)
		imageName = strings.TrimSuffix(imageName, filepath.Ext(imageName))
		fn += "_" + common.SafeStringForCompressString(imageName) + "_" + u.ID
	} else {
		fn += "_" + u.ID
	}

	os.MkdirAll(path, 0777)
	of, err := os.OpenFile(path+fn, os.O_CREATE|os.O_WRONLY, 0777)
	if err != nil {
		return "", err
	}
	defer of.Close()

	_, err = io.Copy(of, dec)
	return "LOCAL:" + fn, err
}

func writeAvatar(u *model.User, image string) (string, error) {
	image = image[strings.Index(image, ",")+1:]
	dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(image))

	hash := u.IDHash()
	path := fmt.Sprintf("tmp/images/%d/", hash%1024)
	fn := fmt.Sprintf("%016x@%s", hash, u.ID)

	os.MkdirAll(path, 0777)
	of, err := os.OpenFile(path+fn, os.O_CREATE|os.O_WRONLY, 0777)
	if err != nil {
		return "", err
	}
	defer of.Close()

	_, err = io.CopyN(of, dec, 150*1024)
	if err == io.EOF {
		err = nil
	}
	return fn, err
}
