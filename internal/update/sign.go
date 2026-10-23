package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// release signing keys, the private halves are kept off github so a taken over
// account can't ship an update. more than one so a new key can be added before
// the old one is retired
var releaseKeys = []string{
	"6p2SKBF9xTq2cfN5qJkcI2vQenGDEFMmQvIKQZNe0R4=", // 2026-10-24
}

// SHA256SUMS of releases made before signing, going back to one of them is checked against these
var pinnedSums = map[string]string{
	"1.0.0":   "082eb5e740293ec7bead200a9c4efb19c7743bff731d6d44b906d6b129e8bb6a",
	"1.1.0":   "6ea300977372071fb5027e34fe9b216a29b72a939349da25f0889e2f4b14b95e",
	"1.1.1":   "af740701b7db2eb09996ea93c9aaf283c0f0a1e1156afe4c57dd04f3a06c81a1",
	"1.2.0":   "703665b7eee0830aa30a17c94a121fd4bdeca75e1938b96419b1a3803a65b2db",
	"1.2.1":   "a49d407df08cb5263f44373395f4cd68b51807b9eb6a93cafa2b88077846de32",
	"1.2.2":   "de9ec4a75a7ecb58ad115a1f35e951a6dd830af0e48a35f3a542f6f80d61ca4f",
	"1.2.3":   "f8387743d2e421959c5dfd15600acc6d44f93574c9d2f2b971136d074969b35e",
	"1.2.4":   "d34d84cd9261aea9b66b2508d202f688055ca1a5e2064b44fbdfa8686bae4657",
	"1.2.5":   "110acf3a9813bd54e0601aae508635fe4d0739fd6c71fc81d5b7fa2e46c7d2e4",
	"1.2.6":   "f98b1576230b285dee2cdf240bf019ac78f27f8f144e4f9d511b8eb70b3e4df0",
	"1.3.0":   "38d15a14bfa1a54f709acdc0861e9e1294469bd2173ca4b7ef63cb5715838c29",
	"1.3.1":   "2b5ea4edf8712a1b1bd8a8331e463b448e0ba75478610a5db7bea4d1cc4c7aa8",
	"1.3.1.1": "59cfe9a6ef86b49bd701caad83d6e25e99ad3f4859597d8c6ded56bf5644fc1e",
	"1.3.1.2": "0f3cb113d7d9a5433add64b262684d6c8cdddacda3e91f5a755d3e7a6a741b01",
	"1.3.1.3": "fb75030b5498eca6d16b137c1fda86908b049bd6a8c67907d951d00fd2834ae9",
	"1.3.1.4": "4aed18ed16baff63bf272da8f480fb125263e01397a7d78bfd64cebc23e2a807",
	"1.3.1.5": "8ca81d75162bc963c59f4e6661a1e4e323a54b024fea1551adfa2b58e2657fe8",
	"1.3.2":   "86c54cd5eb3dc51adaf4661858ea15a4f3420a39754c75f94f65c7b2a56d845f",
	"1.3.2.1": "1b9c403badfda36259210e8bfede0a54703a8e6a45d8e07da87107efe2c96c99",
	"1.3.2.2": "70bff7c079a172606d6cc0aea07310fd0c2fad47ed8f8962515137f809fb6808",
}

// what gets signed, the version is in it so an old signed release can't be passed off as a new one
func SignedMessage(version string, sums []byte) []byte {
	return append([]byte("fengard "+strings.TrimPrefix(version, "v")+"\n"), sums...)
}

// checkSums trusts SHA256SUMS only when one of our keys signed it for this version or it's a pinned old release
func checkSums(version string, sums, sig []byte) error {
	if want, ok := pinnedSums[version]; ok {
		got := sha256.Sum256(sums)
		if hex.EncodeToString(got[:]) != want {
			return errors.New("SHA256SUMS isn't the one Fengard " + version + " was released with")
		}
		return nil
	}
	if len(sig) == 0 {
		return errors.New("the release isn't signed, not installing it")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("the release signature is damaged, not installing it")
	}
	msg := SignedMessage(version, sums)
	for _, k := range releaseKeys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, msg, raw) {
			return nil
		}
	}
	return errors.New("the release isn't signed by the Fengard release key, not installing it")
}
