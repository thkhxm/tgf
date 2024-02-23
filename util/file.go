package util

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

func GetFileList(path string, ext string) []string {
	var all_file []string
	finfo, _ := os.ReadDir(path)
	for _, info := range finfo {
		if filepath.Ext(info.Name()) == ext {
			real_path := path + string(filepath.Separator) + info.Name()
			if info.IsDir() {
				//all_file = append(all_file, getFileList(real_path)...)
			} else {
				all_file = append(all_file, real_path)
			}
		}
	}
	return all_file
}

func GetFileMd5(file string) string {
	f, err := os.OpenFile(file, os.O_RDONLY, 0o600)
	if err != nil {
		return ""
	}
	md5h := md5.New()
	io.Copy(md5h, f)
	f.Close()
	return hex.EncodeToString(md5h.Sum(nil))
}
