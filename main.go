package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/veter2005/bunny-storage-sync/api"
	"github.com/veter2005/bunny-storage-sync/syncer"
)

const version = "1.2.2"

func main() {
	var dryRun, sizeOnly, onlyMissing, deleteRemote, verbose, showVersion bool
	var concurrency int
	var syncPath string

	flag.BoolVar(&dryRun, "dry-run", false, "Show what would be done")
	flag.BoolVar(&sizeOnly, "size-only", false, "Fast comparison by size")
	flag.BoolVar(&onlyMissing, "only-missing", false, "Only upload new files")
	flag.BoolVar(&deleteRemote, "delete", false, "Delete remote files not in local")
	flag.IntVar(&concurrency, "concurrency", 10, "Parallel operations")
	flag.BoolVar(&verbose, "verbose", false, "Enable debug logging")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.StringVar(&syncPath, "path", "", "Subdirectory in zone")
	flag.Parse()

	if showVersion {
		fmt.Printf("BunnyCDN Sync v%s\n", version)
		os.Exit(0)
	}

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}

	apiKey := os.Getenv("BCDN_APIKEY")
	if apiKey == "" {
		fmt.Println("Error: BCDN_APIKEY not set")
		os.Exit(1)
	}

	storage := api.BCDNStorage{
		ZoneName: flag.Arg(1),
		APIKey:   apiKey,
		Verbose:  verbose, 
	}

	syncerService := syncer.BCDNSyncer{
		API:         storage,
		DryRun:      dryRun,
		SizeOnly:    sizeOnly,
		OnlyMissing: onlyMissing,
		Delete:      deleteRemote,
		Concurrency: concurrency,
		Verbose:     verbose,
	}

	if err := syncerService.Sync(flag.Arg(0), syncPath); err != nil {
		fmt.Printf("Sync failed: %v\n", err)
		os.Exit(1)
	}
}
