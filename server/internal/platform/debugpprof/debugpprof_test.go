package debugpprof

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestEnabled(t *testing.T) {
	t.Setenv("ENABLE_PPROF", "")
	if Enabled() {
		t.Fatal("empty should be disabled")
	}
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv("ENABLE_PPROF", value)
		if !Enabled() {
			t.Fatalf("%q should enable pprof", value)
		}
	}
	t.Setenv("ENABLE_PPROF", "0")
	if Enabled() {
		t.Fatal("0 should be disabled")
	}
}

func TestMountServesIndexAndHeap(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux)

	index := httptest.NewRequest(http.MethodGet, PathPrefix+"/", nil)
	indexRec := httptest.NewRecorder()
	mux.ServeHTTP(indexRec, index)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status=%d body=%s", indexRec.Code, indexRec.Body.String())
	}

	heap := httptest.NewRequest(http.MethodGet, PathPrefix+"/heap", nil)
	heapRec := httptest.NewRecorder()
	mux.ServeHTTP(heapRec, heap)
	if heapRec.Code != http.StatusOK {
		t.Fatalf("heap status=%d", heapRec.Code)
	}
}

func TestMaybeMountRespectsEnv(t *testing.T) {
	os.Unsetenv("ENABLE_PPROF")
	mux := http.NewServeMux()
	MaybeMount(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PathPrefix+"/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled mount status=%d, want 404", rec.Code)
	}
}
