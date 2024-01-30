package exp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"strings"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/1/29
//***************************************************

func parseAst() {
	// 解析源文件
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "C:\\Users\\AUSA\\Documents\\IdeaProject\\tgf-example\\common\\service\\dungeon_service.go", nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	// 遍历AST并找到IDungeonService接口的方法签名
	ast.Inspect(node, func(n ast.Node) bool {
		// 查找接口类型
		if t, ok := n.(*ast.TypeSpec); ok {
			if t.Name.Name == "IDungeonService" {
				if iface, ok := t.Type.(*ast.InterfaceType); ok {
					for _, method := range iface.Methods.List {
						// 对每个方法构建签名字符串
						if f, ok := method.Type.(*ast.FuncType); ok {
							var params, results []string
							if f.Params != nil {
								for _, param := range f.Params.List {
									paramType := exprToString(param.Type)
									for _, name := range param.Names {
										params = append(params, name.Name+" "+paramType)
									}
								}
							}
							if f.Results != nil {
								for _, result := range f.Results.List {
									resultType := exprToString(result.Type)
									for _, name := range result.Names {
										results = append(results, name.Name+" "+resultType)
									}
								}
							}
							signature := method.Names[0].Name + "(" + strings.Join(params, ", ") + ") " + strings.Join(results, ", ")
							log.Println(signature)
						}
					}
				}
			}
		}
		return true
	})
}

func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.Ident:
		return e.Name
	case *ast.ArrayType:
		return "[]" + exprToString(e.Elt)
	case *ast.IndexExpr:
		// 添加对泛型类型的基本处理
		return exprToString(e.X) + "[" + exprToString(e.Index) + "]"
	// 可以根据需要继续添加更多的类型转换
	default:
		return ""
	}
}
