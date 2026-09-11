// import.go
//
// Emits import.go for a resource with import_csv: a CSV import handler at
// POST /{res}/import/csv. It parses a multipart file, maps the CSV header to
// the create form's field names, reuses buildCreateParams (defined in
// create.go) for per-row value construction, and runs every insert inside one
// transaction, reporting "Imported N, Skipped M (row: error)" via a ?flash=
// redirect shown in the topbar.
package generator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichalHerstus/yaga/internal/types"
)

// generateImportHandler writes import.go for a resource with import_csv
// enabled. The handler is CSRF-protected by the global middleware and RBAC-
// wrapped by the router using the create permission.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateImportHandler(dir string, r types.Resource) error {
	pkgName := resourcePkgName(r.Name)
	tName := tableName(r)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)

	var colNames []string
	for _, f := range r.Form.Create.Fields {
		colNames = append(colNames, f.Name)
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "encoding/csv"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"

    httperr %q
)

func ImportCSV(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseMultipartForm(32 << 20); err != nil {
            httperr.Internal(w, err)
            return
        }
        file, _, err := r.FormFile("file")
        if err != nil {
            http.Redirect(w, r, %q+"?flash="+url.QueryEscape("Import failed: "+err.Error()), http.StatusFound)
            return
        }
        defer file.Close()

        rd := csv.NewReader(file)
        header, err := rd.Read()
        if err != nil {
            http.Redirect(w, r, %q+"?flash="+url.QueryEscape("Import failed: "+err.Error()), http.StatusFound)
            return
        }
        colIndex := map[string]int{}
        for i, h := range header {
            colIndex[strings.TrimSpace(h)] = i
        }

        cols := []string{%s}
        sqlCols := []string{%s}
        placeholders := make([]string, len(cols))
        for i := range cols {
            placeholders[i] = fmt.Sprintf("$%%d", i+1)
        }
        query := fmt.Sprintf("INSERT INTO %%s (%%s) VALUES (%%s)", %q, strings.Join(sqlCols, ", "), strings.Join(placeholders, ", "))

        inserted, skipped := 0, 0
        var errs []string
        tx, err := db.BeginTx(r.Context(), nil)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer tx.Rollback()

        for rowNum := 1; ; rowNum++ {
            rec, err := rd.Read()
            if err == io.EOF {
                break
            }
            if err != nil {
                skipped++
                errs = append(errs, fmt.Sprintf("row %%d: %%v", rowNum, err))
                continue
            }
            m := map[string]string{}
            for _, name := range cols {
                if i, ok := colIndex[name]; ok && i < len(rec) {
                    m[name] = rec[i]
                }
            }
            vals, err := buildCreateParams(m)
            if err != nil {
                skipped++
                errs = append(errs, fmt.Sprintf("row %%d: %%v", rowNum, err))
                continue
            }
            if _, err := tx.ExecContext(r.Context(), query, vals...); err != nil {
                skipped++
                errs = append(errs, fmt.Sprintf("row %%d: %%v", rowNum, err))
                continue
            }
            inserted++
        }

        if err := tx.Commit(); err != nil {
            httperr.Internal(w, err)
            return
        }

        msg := fmt.Sprintf("Imported %%d, Skipped %%d", inserted, skipped)
        if len(errs) > 0 {
            msg += ": " + strings.Join(errs, "; ")
        }
        http.Redirect(w, r, %q+"?flash="+url.QueryEscape(msg), http.StatusFound)
    }
}
`, pkgName, g.moduleImport("internal/panel/httperr"),
		listPath, listPath,
		colsLiteral(colNames), g.colsLiteralQuoted(colNames), g.quoteIdent(tName), listPath)

	return os.WriteFile(filepath.Join(dir, "import.go"), []byte(code), 0644)
}
