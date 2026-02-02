package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/veter2005/bunny-storage-sync/api"
	"github.com/veter2005/bunny-storage-sync/syncer"
)

func main() {
	var dryRun bool
	var sizeOnly bool
	var onlyMissing bool
	flag.BoolVar(&dryRun, "dry-run", false, "Show the difference and exit")
	flag.BoolVar(&sizeOnly, "size-only", false, "Use only file size for comparison instead of checksum")
	flag.BoolVar(&onlyMissing, "only-missing", false, "Only upload missing files, do not update existing ones")
	flag.Parse()

	src := flag.Arg(0)
	zoneName := flag.Arg(1)
	apiKey := os.Getenv("BCDN_APIKEY")
	if apiKey == "" {
		fmt.Println("API key must be set with BCDN_APIKEY")
		os.Exit(-1)
	}

	fmt.Println("Walking path:", src)
	storage := api.BCDNStorage{ZoneName: zoneName, APIKey: apiKey}
	syncerService := syncer.BCDNSyncer{
		API:         storage,
		DryRun:      dryRun,
		SizeOnly:    sizeOnly,
		OnlyMissing: onlyMissing,
	}

	err := syncerService.Sync(src)
	fmt.Println(err)
}
