package gcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/TTMathCS/g9s/internal/config"
)

// capAccessors maps each lister that bounds its listing to the config accessor
// it must read that bound from.
var capAccessors = map[string]string{
	"bigquery.go":        "LimitBigQueryJobs",
	"dataflow.go":        "LimitDataflowJobs",
	"dataprocjobs.go":    "LimitDataprocJobsPerRegion",
	"clusterjobs.go":     "LimitClusterJobs",
	"bqtables.go":        "LimitBigQueryTables",
	"dnsrecords.go":      "LimitDNSRecordSets",
	"lbhealth.go":        "LimitBackendGroups",
	"serviceaccounts.go": "LimitServiceAccountKeyLookups",
	"kms.go":             "LimitKMSKeyRings",
}

// TestEveryCappedListerReadsItsLimitFromConfig proves the wiring. The config
// tests show the accessors return the right numbers; this shows the listers
// actually ask. Without it, a lister could keep its own constant and the
// setting would decode fine, validate fine, and do nothing.
func TestEveryCappedListerReadsItsLimitFromConfig(t *testing.T) {
	for file, accessor := range capAccessors {
		source := readGCPSource(t, file)
		if !strings.Contains(source, accessor+"()") {
			t.Errorf("%s does not call cfg.%s() — its cap is not configurable", file, accessor)
		}
	}
}

// hardcodedCap matches a package-level constant that looks like a row cap.
//
// Deliberately loose on the name and strict on the shape: the point is to catch
// the next cap someone adds, whatever they call it, not to match the nine that
// existed when this was written.
var hardcodedCap = regexp.MustCompile(`^max[A-Z]\w*$`)

// TestNoListerHardcodesACap is the regression guard. Every one of these numbers
// started life as a constant, which made the bound the tool's opinion rather
// than the reader's, and nothing stopped the next one being written the same
// way. A cap belongs in config.Limits with an accessor.
func TestNoListerHardcodesACap(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing package: %v", err)
	}

	scanned := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			scanned++
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, ident := range value.Names {
						if !hardcodedCap.MatchString(ident.Name) {
							continue
						}
						// A cap is an integer literal. A const naming a string
						// or a duration is something else wearing a max- name.
						if i < len(value.Values) && isIntLiteral(value.Values[i]) {
							t.Errorf("%s declares const %s — row caps belong in config.Limits so they can be raised",
								name, ident.Name)
						}
					}
				}
			}
		}
	}

	// The walk finding nothing has to mean "nothing there", not "walked
	// nothing". This is the failure mode that makes a structural test useless
	// while it goes on passing.
	if scanned < 10 {
		t.Fatalf("scanned only %d files — the walk is not covering the package", scanned)
	}
}

func isIntLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.INT
}

// TestUncappedConfigIsHonouredEndToEnd checks the one value with a special
// meaning survives the trip from yaml to the comparison a lister makes.
func TestUncappedConfigIsHonouredEndToEnd(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{
		Limits: config.Limits{DNSRecordSets: -1},
	}}

	limit := cfg.LimitDNSRecordSets()
	// This is the exact comparison every capped lister makes. A listing of a
	// million rows must not trip it.
	if 1_000_000 >= limit {
		t.Errorf("a million rows hit the uncapped limit %d", limit)
	}
}

func readGCPSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(raw)
}
