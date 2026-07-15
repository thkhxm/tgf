package util

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
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
	digest, _ := GetFileMd5WithError(file)
	return digest
}

// GetFileMd5WithError returns the file's MD5 digest and reports read/close errors.
func GetFileMd5WithError(file string) (string, error) {
	f, err := os.OpenFile(file, os.O_RDONLY, 0o600)
	if err != nil {
		return "", err
	}
	md5h := md5.New()
	if _, err := io.Copy(md5h, f); err != nil {
		_ = f.Close() // Preserve the copy error; closing a read-only file has no recovery path.
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(md5h.Sum(nil)), nil
}

func executeTemplateAndClose(file *os.File, tmpl *template.Template, data any) {
	if err := tmpl.Execute(file, data); err != nil {
		_ = file.Close() // Preserve the template execution error as the primary failure.
		panic(fmt.Errorf("execute template: %w", err))
	}
	if err := file.Close(); err != nil {
		panic(fmt.Errorf("close generated file: %w", err))
	}
}
