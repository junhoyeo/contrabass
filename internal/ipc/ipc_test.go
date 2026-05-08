package ipc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportedTypesHaveGodocComments(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "ipc.go", nil, parser.ParseComments)
	require.NoError(t, err)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || !typeSpec.Name.IsExported() {
				continue
			}

			comment := ""
			if genDecl.Doc != nil {
				comment = genDecl.Doc.Text()
			}

			require.Truef(t, strings.HasPrefix(comment, typeSpec.Name.Name),
				"exported type %s should have a godoc comment starting with its name", typeSpec.Name.Name)
		}
	}
}
