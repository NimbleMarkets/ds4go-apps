package main

import (
	"image"
	"testing"

	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
)

func testImg() image.Image { return image.NewRGBA(image.Rect(0, 0, 4, 4)) }

// TestCachePutGet verifies a stored bitmap is retrievable by key.
func TestCachePutGet(t *testing.T) {
	c := newWidgetCache(8)
	img := testImg()
	c.Put("a.svg", cachedWidget{img: img})
	got, ok := c.Get("a.svg")
	if !ok {
		t.Fatal("Get(a.svg) = ok false, want true")
	}
	if got.img != image.Image(img) {
		t.Error("Get returned a different image than Put stored")
	}
}

// TestCacheGetMissing verifies an unknown key reports a miss.
func TestCacheGetMissing(t *testing.T) {
	c := newWidgetCache(8)
	if _, ok := c.Get("nope.svg"); ok {
		t.Error("Get on empty cache reported a hit")
	}
}

// TestCacheEvictsLRU verifies the least-recently-used entry is dropped
// once the cache is over capacity.
func TestCacheEvictsLRU(t *testing.T) {
	c := newWidgetCache(2)
	c.Put("a.svg", cachedWidget{img: testImg()})
	c.Put("b.svg", cachedWidget{img: testImg()})
	c.Put("c.svg", cachedWidget{img: testImg()}) // evicts a.svg, the oldest
	if _, ok := c.Get("a.svg"); ok {
		t.Error("a.svg should have been evicted")
	}
	if _, ok := c.Get("b.svg"); !ok {
		t.Error("b.svg should still be cached")
	}
	if _, ok := c.Get("c.svg"); !ok {
		t.Error("c.svg should still be cached")
	}
}

// TestCacheGetRefreshesRecency verifies that reading a key marks it as
// recently used, so it survives the next eviction.
func TestCacheGetRefreshesRecency(t *testing.T) {
	c := newWidgetCache(2)
	c.Put("a.svg", cachedWidget{img: testImg()})
	c.Put("b.svg", cachedWidget{img: testImg()})
	c.Get("a.svg")                               // a.svg is now most-recently-used
	c.Put("c.svg", cachedWidget{img: testImg()}) // should evict b.svg, not a.svg
	if _, ok := c.Get("a.svg"); !ok {
		t.Error("a.svg was touched by Get and should have survived eviction")
	}
	if _, ok := c.Get("b.svg"); ok {
		t.Error("b.svg was least-recently-used and should have been evicted")
	}
}

// TestLoadEntryUsesCachedBitmap verifies navigating to an entry whose
// bitmap is cached installs it directly (no async reload).
func TestLoadEntryUsesCachedBitmap(t *testing.T) {
	img := testImg()
	m := &model{
		cache:     newWidgetCache(8),
		svgWidget: svg.New(80, 24),
		entries:   []svgEntry{{filename: "a.svg", svgData: sampleSVGData}},
	}
	m.entryIndex = 0
	m.cache.Put("a.svg", cachedWidget{img: img})

	m.loadEntryCmd()

	if m.svgWidget.Image() != image.Image(img) {
		t.Error("loadEntryCmd did not install the cached bitmap")
	}
}

// TestLoadEntryReloadsUncachedEntry verifies navigating to an entry with
// no cached bitmap starts a fresh load, clearing any stale image.
func TestLoadEntryReloadsUncachedEntry(t *testing.T) {
	m := &model{
		cache:     newWidgetCache(8),
		svgWidget: svg.New(80, 24),
		entries:   []svgEntry{{filename: "b.svg", svgData: sampleSVGData}},
	}
	m.entryIndex = 0
	m.svgWidget.SetImage(testImg()) // stale image from a previous entry

	m.loadEntryCmd()

	if m.svgWidget.Image() != nil {
		t.Error("loadEntryCmd on an uncached entry left a stale image")
	}
}

var sampleSVGData = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="10" viewBox="0 0 20 10"><rect width="20" height="10" fill="red"/></svg>`)
