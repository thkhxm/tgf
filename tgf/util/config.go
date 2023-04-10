package util

import (
	"fmt"
	"github.com/xuri/excelize/v2"
	"os"
	"path/filepath"
	"text/template"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

var (
	excelToJsonPath = "./export/json/"
	excelToGoPath   = "./export/go/"
	excelPath       = "./conf/excel/"
	goPackage       = "config"
)

// ExcelToJson
// @Description: Excel导出json文件
func ExcelToJson() {
	files := getFileList(excelPath)
	fmt.Println("files:", files)
	for _, file := range files {
		structs := parseFile(file)
		toGolang(structs)
	}
}

// ExcelToGo
// @Description: Excel导出.go文件
func ExcelToGo() {

}

// SetExcelToJsonPath
// @Description: 设置Excel导出Json地址
// @param path
func SetExcelToJsonPath(path string) {
	excelToJsonPath = path
	//log.InfoTag("conf", "set excel to json path %v", path)
}

// SetExcelToGoPath
// @Description: 设置Excel导出Go地址
// @param path
func SetExcelToGoPath(path string) {
	excelToGoPath = path
	//log.InfoTag("conf", "set excel to go path %v", path)
}

// SetExcelPath
// @Description: 设置Excel文件所在路径
func SetExcelPath(path string) {
	excelPath = path
	//log.InfoTag("conf", "set excel file path %v", path)
}

// to golang
func toGolang(metalist []*configStruct) {
	tpl := fmt.Sprintf(`package %v
		{{range .}}
type {{.StructName}}Conf struct {
		{{range .Fields}}
		//{{.Des}}
		{{.Key}}	{{.Typ}}
        {{end}}{{end}}
}`, goPackage)
	t := template.New("ConfigStruct")
	tp, _ := t.Parse(tpl)
	file, err := os.Create("data.goti")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	tp.Execute(file, metalist)
}

//

func getFileList(path string) []string {
	var all_file []string
	finfo, _ := os.ReadDir(path)
	for _, info := range finfo {
		if filepath.Ext(info.Name()) == ".xlsx" {
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

type configStruct struct {
	StructName string
	Fields     []*meta
	Version    string
}

type meta struct {
	Key string
	Idx int
	Typ string
	Des string
}

type rowdata []interface{}

func parseFile(file string) []*configStruct {

	fmt.Println("\n\n\n\n", file)

	xlsx, err := excelize.OpenFile(file)
	if err != nil {
		panic(err.Error())
	}
	sheets := xlsx.GetSheetList()

	rs := make([]*configStruct, len(sheets))

	for i, s := range sheets {
		rows, err := xlsx.GetRows(s)
		if err != nil {
			return nil
		}
		if len(rows) < 5 {
			return nil
		}

		colNum := len(rows[1])
		fmt.Println("col num:", colNum)
		metaList := make([]*meta, 0, colNum)
		dataList := make([]rowdata, 0, len(rows)-4)
		version := ""
		for line, row := range rows {
			switch line {
			case 0: // sheet 名
				version = row[0]
			case 1: // col name
				for idx, colname := range row {
					metaList = append(metaList, &meta{Key: colname, Idx: idx})
				}
			case 2: // data type
				for idx, typ := range row {
					metaList[idx].Typ = typ
				}
			case 3: // desc
				for idx, des := range row {
					metaList[idx].Des = des
				}
			default: //>= 4 row data
				data := make(rowdata, colNum)
				for k := 0; k < colNum; k++ {
					if k < len(row) {
						data[k] = row[k]
					}
				}
				dataList = append(dataList, data)
			}
		}
		jsonFile := fmt.Sprintf("%s.json", s)
		err = output(jsonFile, toJson(dataList, metaList))
		if err != nil {
			fmt.Println(err)
		}
		result := &configStruct{}
		result.Fields = metaList
		result.StructName = s
		result.Version = version
		rs[i] = result
		fmt.Println("jsonFile:", jsonFile, " ["+version, "]")
	}
	return rs
}

const (
	fileType_string     = "string"
	fileType_time       = "time"
	fileType_arrayInt32 = "[]int32"
)

func toJson(datarows []rowdata, metalist []*meta) string {
	ret := "["
	for _, row := range datarows {
		ret += "\n\t{"
		for idx, meta := range metalist {
			ret += fmt.Sprintf("\n\t\t\"%s\":", meta.Key)
			switch meta.Typ {
			case fileType_string:
				if row[idx] == nil {
					ret += "\"\""
				} else {
					ret += fmt.Sprintf("\"%s\"", row[idx])
				}
			case fileType_time:
			case fileType_arrayInt32:
				if row[idx] == nil || row[idx] == "" {
					ret += "[]"
				} else {
					ret += fmt.Sprintf("%s", row[idx])
				}
			default:
				if row[idx] == nil || row[idx] == "" {
					ret += "0"
				} else {
					ret += fmt.Sprintf("%s", row[idx])
				}
			}
			ret += ","
		}
		ret = ret[:len(ret)-1]
		ret += "\n\t},"
	}
	ret = ret[:len(ret)-1]
	ret += "\n]"
	return ret
}

func output(filename string, str string) error {

	f, err := os.OpenFile(excelToJsonPath+filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(str)
	if err != nil {
		return err
	}

	return nil
}
