// Command refscan 用 AST 统计「零生产引用的函数」与「超长函数」。
//
// 用途：代码审查/重构后确认某个函数是否还有人调用。比正则扫描可靠——
// 正则剥字符串时会误吃注释里的反引号（本仓库注释大量使用 `file.go:123` 形式），
// 导致调用点被漏计。
//
// 用法：go run ./tools/refscan
//
// 判据：ident 在**非测试文件**中的出现次数 == 该名字的函数定义数，即只有定义、没有任何引用。
// 注意这是名字级统计：同名方法定义在多个接收者上时会算作多次定义，需要人工复核。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fnInfo struct {
	file  string
	recv  string
	name  string
	start int
	end   int
	depth int
}

func main() {
	fset := token.NewFileSet()
	prod := map[string]int{}
	test := map[string]int{}
	var fns []fnInfo
	files := 0

	for _, root := range []string{"internal", "cmd"} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			files++
			isTest := strings.HasSuffix(p, "_test.go")
			f, perr := parser.ParseFile(fset, p, nil, 0)
			if perr != nil {
				fmt.Println("PARSE FAIL", p, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || id.Name == "_" {
					return true
				}
				if isTest {
					test[id.Name]++
				} else {
					prod[id.Name]++
				}
				return true
			})
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				recv := ""
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					recv = typeName(fd.Recv.List[0].Type)
				}
				fns = append(fns, fnInfo{
					file:  strings.ReplaceAll(p, "\\", "/"),
					recv:  recv,
					name:  fd.Name.Name,
					start: fset.Position(fd.Pos()).Line,
					end:   fset.Position(fd.End()).Line,
					depth: blockDepth(fd.Body),
				})
			}
			return nil
		})
	}

	defs := map[string]int{}
	for _, f := range fns {
		defs[f.name]++
	}

	fmt.Printf("扫描 %d 个文件，%d 个函数\n\n", files, len(fns))
	fmt.Println("=== 零生产引用的函数（prod == 定义数；排除 Test/Benchmark/Example/main/init）===")
	var zero []fnInfo
	for _, f := range fns {
		n := f.name
		if strings.HasSuffix(f.file, "_test.go") ||
			strings.HasPrefix(n, "Test") || strings.HasPrefix(n, "Benchmark") ||
			strings.HasPrefix(n, "Example") || strings.HasPrefix(n, "Fuzz") ||
			n == "main" || n == "init" {
			continue
		}
		if prod[n] > 0 && prod[n] == defs[n] {
			zero = append(zero, f)
		}
	}
	sort.Slice(zero, func(i, j int) bool {
		if zero[i].file != zero[j].file {
			return zero[i].file < zero[j].file
		}
		return zero[i].start < zero[j].start
	})
	total := 0
	for _, f := range zero {
		n := f.end - f.start + 1
		total += n
		mark := ""
		if test[f.name] > 0 {
			mark = fmt.Sprintf("（测试引用 %d 次）", test[f.name])
		}
		fmt.Printf("%-46s %-26s %4d 行%s\n", f.file, f.name, n, mark)
	}
	fmt.Printf("合计 %d 个函数 / %d 行\n\n", len(zero), total)

	sort.Slice(fns, func(i, j int) bool { return fns[i].end-fns[i].start > fns[j].end-fns[j].start })
	fmt.Println("=== 最长的 15 个函数（行数 / 控制流嵌套层数）===")
	for i := 0; i < 15 && i < len(fns); i++ {
		f := fns[i]
		fmt.Printf("%4d 行  d=%d  %s:%d  %s.%s\n", f.end-f.start+1, f.depth, f.file, f.start, f.recv, f.name)
	}
}

func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return typeName(t.X)
	}
	return "?"
}

// blockDepth 统计函数体内 If/For/Range/Switch/TypeSwitch/Select 的最大嵌套层数（函数体记 1 层）。
func blockDepth(body *ast.BlockStmt) int {
	best := 1
	var walk func(n ast.Node, d int)
	walk = func(n ast.Node, d int) {
		if n == nil {
			return
		}
		if d > best {
			best = d
		}
		switch x := n.(type) {
		case *ast.IfStmt:
			walk(x.Body, d+1)
			if x.Else != nil {
				if b, ok := x.Else.(*ast.BlockStmt); ok {
					walk(b, d+1)
				} else {
					walk(x.Else, d) // else if 不额外加深
				}
			}
			return
		case *ast.ForStmt:
			walk(x.Body, d+1)
			return
		case *ast.RangeStmt:
			walk(x.Body, d+1)
			return
		case *ast.SwitchStmt:
			walk(x.Body, d+1)
			return
		case *ast.TypeSwitchStmt:
			walk(x.Body, d+1)
			return
		case *ast.SelectStmt:
			walk(x.Body, d+1)
			return
		case *ast.CaseClause:
			for _, s := range x.Body {
				walk(s, d)
			}
			return
		case *ast.CommClause:
			for _, s := range x.Body {
				walk(s, d)
			}
			return
		}
		ast.Inspect(n, func(c ast.Node) bool {
			if c == nil || c == n {
				return c == n
			}
			switch c.(type) {
			case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
				*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
				walk(c, d)
				return false
			}
			return true
		})
	}
	for _, s := range body.List {
		walk(s, 1)
	}
	return best
}
