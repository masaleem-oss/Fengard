package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	pub, priv, _ := ed25519.GenerateKey(nil)
	old := releaseKeys
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	t.Cleanup(func() { releaseKeys = old })
	return priv
}

func sign(k ed25519.PrivateKey, version string, sums []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(k, SignedMessage(version, sums))) + "\n")
}

func TestCheckSums(t *testing.T) {
	k := testKey(t)
	_, other, _ := ed25519.GenerateKey(nil)
	sums := []byte("abc  fengardd-linux-arm64.gz\n")
	if err := checkSums("1.3.3", sums, sign(k, "1.3.3", sums)); err != nil {
		t.Fatalf("good signature refused: %v", err)
	}
	bad := map[string][]byte{
		"unsigned":      nil,
		"other key":     sign(other, "1.3.3", sums),
		"other version": sign(k, "1.3.2.9", sums),
		"damaged":       []byte("not base64!"),
	}
	for name, sig := range bad {
		if err := checkSums("1.3.3", sums, sig); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := checkSums("1.3.3", []byte("def  fengardd-linux-arm64.gz\n"), sign(k, "1.3.3", sums)); err == nil {
		t.Error("changed SHA256SUMS accepted")
	}
}

func TestPinnedOldReleases(t *testing.T) {
	sums := []byte("old sums\n")
	h := sha256.Sum256(sums)
	pinnedSums["0.0.1"] = hex.EncodeToString(h[:])
	defer delete(pinnedSums, "0.0.1")
	if err := checkSums("0.0.1", sums, nil); err != nil {
		t.Fatalf("pinned release refused: %v", err)
	}
	if err := checkSums("0.0.1", []byte("swapped\n"), nil); err == nil {
		t.Fatal("swapped SHA256SUMS of a pinned release accepted")
	}
	for v, want := range pinnedSums {
		if len(want) != 64 || Compare(v, "1.3.3") >= 0 {
			t.Errorf("pin %s = %q", v, want)
		}
	}
}

func TestWantSumNeedsSignature(t *testing.T) {
	k := testKey(t)
	sums := []byte("0123  fengardd-linux-arm64.gz\n")
	sig := sign(k, "1.3.3", sums)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sums":
			w.Write(sums)
		case "/sig":
			w.Write(sig)
		}
	}))
	defer srv.Close()
	u := &Updater{Client: srv.Client(), Current: "1.3.2"}
	rel := &release{Tag: "v1.3.3", Assets: []asset{{Name: "SHA256SUMS", URL: srv.URL + "/sums"}}}
	if _, err := u.wantSum(context.Background(), rel, "fengardd-linux-arm64.gz"); err == nil || !strings.Contains(err.Error(), "signed") {
		t.Fatalf("unsigned release: err = %v", err)
	}
	rel.Assets = append(rel.Assets, asset{Name: "SHA256SUMS.sig", URL: srv.URL + "/sig"})
	if got, err := u.wantSum(context.Background(), rel, "fengardd-linux-arm64.gz"); err != nil || got != "0123" {
		t.Fatalf("signed release: %q %v", got, err)
	}
	// the same signed files under a newer tag
	rel.Tag = "v1.9.9"
	if _, err := u.wantSum(context.Background(), rel, "fengardd-linux-arm64.gz"); err == nil {
		t.Fatal("signed files reused under another version were accepted")
	}
}
