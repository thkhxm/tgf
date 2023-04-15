package util

import (
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
			real_path := path + "/" + info.Name()
			if info.IsDir() {
				//all_file = append(all_file, getFileList(real_path)...)
			} else {
				all_file = append(all_file, real_path)
			}
		}
	}
	return all_file
}
