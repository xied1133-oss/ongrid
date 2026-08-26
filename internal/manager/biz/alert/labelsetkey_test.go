package alert

import "testing"

// The same alert subject reported with vs without the ongrid_source
// provenance label (embedded vs cloud collector) must dedupe to ONE incident
// — that's the "duplicate alerts" merge. Identity labels still distinguish.
func TestLabelSetKey_IgnoresProvenanceLabels(t *testing.T) {
	withSrc := map[string]string{"device_id": "1", "mountpoint": "/", "ongrid_source": "embedded"}
	without := map[string]string{"device_id": "1", "mountpoint": "/"}
	if labelSetKey(withSrc) != labelSetKey(without) {
		t.Errorf("ongrid_source must not split the key: with=%q without=%q", labelSetKey(withSrc), labelSetKey(without))
	}
	withProductSrc := map[string]string{"device_id": "1", "mountpoint": "/", "source_id": "embedded"}
	if labelSetKey(withProductSrc) != labelSetKey(without) {
		t.Errorf("source_id must not split the key: with=%q without=%q", labelSetKey(withProductSrc), labelSetKey(without))
	}

	// __name__ is still stripped; real identity labels still distinguish.
	if got := labelSetKey(map[string]string{"__name__": "node_x", "device_id": "1"}); got != "device_id=1" {
		t.Errorf("__name__ should be stripped, got %q", got)
	}
	if labelSetKey(map[string]string{"device_id": "1"}) == labelSetKey(map[string]string{"device_id": "2"}) {
		t.Error("different device_id must produce different keys")
	}
}
