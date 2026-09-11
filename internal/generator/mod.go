// mod.go
//
// Generates the go.mod for the generated admin panel application. The module
// name is the base name of the output directory and the file pins the templ,
// chi, gorilla/sessions and golang.org/x/crypto dependencies.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
)

// generateGoMod writes go.mod for the generated project, using the base name
// of g.OutDir as the module name and the pinned dependency versions from the
// AGENTS.md guide. The matching database driver is added per driver: pgx for
// postgres, modernc.org/sqlite for sqlite, go-mssqldb for mssql.
// Returns an error on write failure.
func (g *Generator) generateGoMod() error {
	modName := filepath.Base(g.OutDir)
	driverDep := ""
	if g.isSQLite() {
		driverDep = "\tmodernc.org/sqlite v1.55.0\n"
	} else if g.isMSSQL() {
		driverDep = "\tgithub.com/microsoft/go-mssqldb v1.10.0\n"
	} else {
		driverDep = "\tgithub.com/jackc/pgx/v5 v5.10.0\n"
	}
	luaDep := ""
	if g.hasAnyScript() {
		luaDep = "\tgithub.com/yuin/gopher-lua v1.1.1\n"
	}
	code := fmt.Sprintf(`module %s

go 1.26

tool github.com/a-h/templ/cmd/templ

require (
	github.com/a-h/templ v0.3.819
	github.com/go-chi/chi/v5 v5.3.1
	github.com/gorilla/sessions v1.4.0
%s%s	golang.org/x/crypto v0.31.0
)
`, modName, driverDep, luaDep)

	return os.WriteFile(filepath.Join(g.OutDir, "go.mod"), []byte(code), 0644)
}
