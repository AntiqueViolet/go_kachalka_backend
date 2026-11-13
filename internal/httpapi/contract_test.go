package httpapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestContractMatchesMiddlewareTables keeps protectedOps and the limiter map in sync with
// api/openapi.yaml. The spec is read from disk, not embedded: the server binary does not need
// an OpenAPI parser.
func TestContractMatchesMiddlewareTables(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{} // generated operation name -> has security
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" {
				t.Fatalf("%s %s has no operationId", method, path)
			}
			name := strings.ToUpper(op.OperationID[:1]) + op.OperationID[1:]
			ops[name] = op.Security != nil && len(*op.Security) > 0
		}
	}
	for name, secured := range ops {
		if secured != protectedOps[name] {
			t.Errorf("%s: spec security=%v, protectedOps=%v", name, secured, protectedOps[name])
		}
	}
	for name := range protectedOps {
		if _, ok := ops[name]; !ok {
			t.Errorf("protectedOps has unknown operation %q", name)
		}
	}
	for name := range ipLimits(Limits{}) {
		if _, ok := ops[name]; !ok {
			t.Errorf("limiter map has unknown operation %q", name)
		}
	}
}
