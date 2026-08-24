package precise

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// precisePeakHeapCeilingBytes bounds the peak Go heap growth of a single
// precise Enrich over a small fixture whose dependency CLOSURE is large
// (net/http + crypto/tls + encoding/json) but whose own source is tiny.
//
// WHY THIS CEILING, AND WHY IT CAN FAIL. Precise enrichment must build SSA
// only for the repository's own packages (ssautil.Packages), not the whole
// transitive dependency closure (ssautil.AllPackages). The dependency
// packages are type-checked by go/packages either way — that cost is fixed —
// but building their SSA bodies and running CHA over them is what blew
// identuum-idp-oss to ~108 GB RSS (SIGKILL) before v1.6.3. This fixture
// imports std packages with deep SSA closures precisely so that a regression
// to whole-program SSA re-materialises megabytes-to-gigabytes of dependency
// SSA and CHA nodes and trips this ceiling. Measured on the scoped build the
// peak heap growth is well under 512 MiB; the ceiling sits far above that
// headroom yet far below any whole-program build, so it distinguishes the two
// without flaking. Red-proved by temporarily restoring ssautil.AllPackages:
// the same fixture then exceeds the ceiling. A guard that cannot fail is not
// one — this one fails the instant precise enrichment stops scoping its SSA.
// preciseAllocCeilingBytes bounds the bytes ALLOCATED by one precise Enrich
// over the guard fixture below. Calibrated by measurement (go 1.27, darwin
// arm64): the scoped build (ssautil.Packages, v1.6.3) allocates ~1305 MiB;
// the whole-program build (ssautil.AllPackages, the pre-fix OOM regression)
// allocates ~1903 MiB on the SAME fixture. The two are separated because the
// go/packages type-check floor (~1.3 GiB) is scope-independent, while the SSA
// bodies + CHA of the imported dependency closure are built only by the
// whole-program path. The ceiling sits in the gap with headroom for the
// scoped build and below the whole-program build, so the guard FAILS the
// instant precise enrichment stops scoping its SSA. Red-proved 2026-08-24 by
// temporarily restoring ssautil.AllPackages: this test then reports ~1903 MiB
// and trips the ceiling. A unit fixture cannot reproduce the 108 GB seen on
// identuum-idp-oss (that needs a large real dependency tree), but the fixture
// need only be big enough to make whole-program SSA measurably exceed scoped
// SSA — which it does — for the guard to detect the regression class.
const preciseAllocCeilingBytes = 1700 << 20 // 1700 MiB

// The fixture's OWN source is tiny, but it imports several std packages with
// deep SSA closures (net/http, crypto/tls, crypto/x509, database/sql,
// text/template, html/template, encoding/xml, go/parser, regexp,
// compress/flate). go/packages type-checks all of them under both SSA scopes
// (a fixed floor), so the only thing that separates scoped from whole-program
// is whether those closures' SSA BODIES are built — which is the regression.
// The larger the imported SSA closure, the larger the whole-program overshoot
// above the scoped build, giving the ceiling robust, non-flaky headroom.
const memoryGuardFixtureSource = `package guardfixture

import (
	"compress/flate"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"go/parser"
	"html/template"
	"net/http"
	"regexp"
	texttemplate "text/template"
)

type Handler interface{ Serve(*http.Request) ([]byte, error) }

type jsonHandler struct{}

func (jsonHandler) Serve(r *http.Request) ([]byte, error) { return json.Marshal(r.URL.String()) }

type tlsHandler struct{ cfg *tls.Config }

func (h tlsHandler) Serve(r *http.Request) ([]byte, error) { _ = h.cfg; return []byte(r.Method), nil }

func dispatch(h Handler, r *http.Request) ([]byte, error) { return h.Serve(r) }

// touchClosures references each heavy import so it stays in the dependency
// closure the SSA builder must (or must not) construct.
func touchClosures() {
	_ = flate.BestCompression
	_ = x509.NewCertPool
	_ = sql.Drivers
	_ = xml.Marshal
	_ = parser.ParseFile
	_ = regexp.MustCompile
	_ = template.HTMLEscapeString
	_ = texttemplate.HTMLEscapeString
}

func Run(r *http.Request) ([][]byte, error) {
	touchClosures()
	hs := []Handler{jsonHandler{}, tlsHandler{cfg: &tls.Config{}}}
	var out [][]byte
	for _, h := range hs {
		b, err := dispatch(h, r)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
`

// TestPreciseBuildStaysUnderPeakHeapCeiling fails when a precise Enrich over a
// tiny fixture with a large dependency closure exceeds precisePeakHeapCeilingBytes
// of peak heap growth — the regression guard for the whole-program-SSA OOM.
func TestPreciseBuildStaysUnderPeakHeapCeiling(t *testing.T) {
	root := writePreciseFixture(t, "example.com/guardfixture", "guard.go", memoryGuardFixtureSource)
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/guardfixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Metric: cumulative bytes ALLOCATED during Enrich (TotalAlloc delta).
	// Peak live heap is dominated by the go/packages type-checking floor,
	// which is identical whether or not dependency SSA is built, so it does
	// not isolate the regression. Whole-program SSA + CHA over the dependency
	// closure instead shows up as a large volume of transient allocation —
	// the churn that drove peak memory footprint to hundreds of GB on
	// identuum-idp-oss. TotalAlloc is monotonic and GC-timing-independent, so
	// it is a stable, non-flaky signal for that churn. A background sampler
	// also records peak HeapInuse for the log.
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var (
		mu       sync.Mutex
		peak     uint64
		stopSamp = make(chan struct{})
		sampDone = make(chan struct{})
	)
	go func() {
		defer close(sampDone)
		var ms runtime.MemStats
		tick := time.NewTicker(1 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopSamp:
				return
			case <-tick.C:
				runtime.ReadMemStats(&ms)
				mu.Lock()
				if ms.HeapInuse > peak {
					peak = ms.HeapInuse
				}
				mu.Unlock()
			}
		}
	}()

	err := Enrich(root, emptyGraph())

	close(stopSamp)
	<-sampDone
	requirePreciseEnrich(t, err)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - base.TotalAlloc

	mu.Lock()
	peakInuse := peak
	mu.Unlock()
	var peakGrowth uint64
	if peakInuse > base.HeapInuse {
		peakGrowth = peakInuse - base.HeapInuse
	}

	t.Logf("precise over guard fixture: allocated %d MiB (ceiling %d MiB), peak-heap-growth %d MiB",
		allocated>>20, preciseAllocCeilingBytes>>20, peakGrowth>>20)
	if allocated > preciseAllocCeilingBytes {
		t.Fatalf("precise build allocated %d MiB > ceiling %d MiB — precise "+
			"enrichment is likely building SSA for the whole dependency closure "+
			"again (ssautil.Packages vs AllPackages)",
			allocated>>20, preciseAllocCeilingBytes>>20)
	}
}
