// go run ./tools/featurepack            rewrites internal/features/manifest.json from features/
// go run ./tools/featurepack -out dir   also writes feature-<id>.tar.gz there for a release
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/features"
)

func main() {
	out := flag.String("out", "", "folder for the add-on archives")
	flag.Parse()
	packs, err := features.Scan("features", config.Features)
	if err != nil {
		log.Fatal(err)
	}
	b, err := json.MarshalIndent(packs, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("internal", "features", "manifest.json"), append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	for _, p := range packs {
		log.Printf("%-12s %3d files, %6.1f KB download", p.ID, len(p.Files), float64(p.Download)/1024)
		if *out == "" {
			continue
		}
		a, err := features.Build("features", &p)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*out, features.Archive(p.ID)), a, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
