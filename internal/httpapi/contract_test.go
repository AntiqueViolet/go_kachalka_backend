package httpapi

import (
	"strings"
	"testing"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi/oapi"
)

// TestContractMatchesMiddlewareTables keeps protectedOps and the limiter map in sync with
// api/openapi.yaml (embedded into the generated code).
func TestContractMatchesMiddlewareTables(t *testing.T) {
	spec, err := oapi.GetSpec()
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
