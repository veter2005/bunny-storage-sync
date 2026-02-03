package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/veter2005/bunny-storage-sync/api"
	"github.com/veter2005/bunny-storage-sync/syncer"
)

const version = "1.2.0"

func main() {
	var dryRun bool
	var sizeOnly bool
	var onlyMissing bool
	var deleteRemote bool
	var concurrency int
	var verbose bool
	var showVersion bool
	var syncPath string

	flag.BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	flag.BoolVar(&sizeOnly, "size-only", false, "Use only file size for comparison instead of checksum")
	flag.BoolVar(&onlyMissing, "only-missing", false, "Only upload missing files, do not update existing ones")
	flag.BoolVar(&deleteRemote, "delete", false, "Delete remote files that don't exist locally (dangerous!)")
	flag.IntVar(&concurrency, "concurrency", 5, "Number of concurrent upload/delete operations")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose debug logging")
	flag.BoolVar(&showVersion, "version", false, "Show version information")
	flag.StringVar(&syncPath, "path", "", "Sync to specific subdirectory in storage zone (e.g., 'subfolder' or 'path/to/dir')")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "BunnyCDN Storage Sync Tool v%s\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <source-path> <zone-name>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  source-path    Local directory to sync\n")
		fmt.Fprintf(os.Stderr, "  zone-name      BunnyCDN storage zone name\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  BCDN_APIKEY    BunnyCDN API key (required)\n\n")
		fmt.Fprintf(os.Stderr, "Safety Notes:\n")
		fmt.Fprintf(os.Stderr, "  By default, this tool only uploads and updates files.\n")
		fmt.Fprintf(os.Stderr, "  Use --delete flag to remove remote files that don't exist locally.\n")
		fmt.Fprintf(os.Stderr, "  Always test with --dry-run first!\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  # Safe sync - only upload/update (recommended)\n")
		fmt.Fprintf(os.Stderr, "  %s ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Full mirror sync - delete remote files not in local\n")
		fmt.Fprintf(os.Stderr, "  %s --delete ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Sync to subdirectory in zone\n")
		fmt.Fprintf(os.Stderr, "  %s --path=subdirectory ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Dry run to see what would be synced\n")
		fmt.Fprintf(os.Stderr, "  %s --dry-run ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Dry run with delete to see what would be removed\n")
		fmt.Fprintf(os.Stderr, "  %s --dry-run --delete ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Sync with verbose output\n")
		fmt.Fprintf(os.Stderr, "  %s --verbose ./website my-zone\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  # Upload only missing files (no updates)\n")
		fmt.Fprintf(os.Stderr, "  %s --only-missing ./website my-zone\n\n", filepath.Base(os.Args[0]))
	}

	flag.Parse()

	// Show version and exit
	if showVersion {
		fmt.Printf("BunnyCDN Storage Sync Tool v%s\n", version)
		os.Exit(0)
	}

	// Validate arguments
	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "Error: Missing required arguments\n\n")
		flag.Usage()
		os.Exit(1)
	}

	src := flag.Arg(0)
	zoneName := flag.Arg(1)

	// Validate source path
	if src == "" {
		fmt.Fprintf(os.Stderr, "Error: Source path cannot be empty\n")
		os.Exit(1)
	}

	// Check if source path exists
	srcInfo, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Source path does not exist: %s\n", src)
		} else {
			fmt.Fprintf(os.Stderr, "Error: Cannot access source path: %v\n", err)
		}
		os.Exit(1)
	}

	// Ensure source is a directory
	if !srcInfo.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: Source path must be a directory: %s\n", src)
		os.Exit(1)
	}

	// Validate zone name
	if zoneName == "" {
		fmt.Fprintf(os.Stderr, "Error: Zone name cannot be empty\n")
		os.Exit(1)
	}

	// Get API key from environment
	apiKey := os.Getenv("BCDN_APIKEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: BCDN_APIKEY environment variable must be set\n")
		fmt.Fprintf(os.Stderr, "Example: export BCDN_APIKEY=your-api-key-here\n")
		os.Exit(1)
	}

	// Validate concurrency
	if concurrency < 1 {
		fmt.Fprintf(os.Stderr, "Error: Concurrency must be at least 1\n")
		os.Exit(1)
	}

	// Print configuration
	fmt.Printf("BunnyCDN Storage Sync v%s\n", version)
	fmt.Printf("=======================\n")
	fmt.Printf("Source path:  %s\n", src)
	fmt.Printf("Zone name:    %s\n", zoneName)
	if syncPath != "" {
		fmt.Printf("Sync path:    %s\n", syncPath)
	} else {
		fmt.Printf("Sync path:    / (root)\n")
	}
	fmt.Printf("Dry run:      %v\n", dryRun)
	fmt.Printf("Delete mode:  %v\n", deleteRemote)
	fmt.Printf("Size only:    %v\n", sizeOnly)
	fmt.Printf("Only missing: %v\n", onlyMissing)
	fmt.Printf("Concurrency:  %d\n", concurrency)
	fmt.Printf("Verbose:      %v\n", verbose)
	fmt.Printf("=======================\n\n")

	if dryRun {
		fmt.Println("*** DRY RUN MODE - No changes will be made ***\n")
	}
	
	if !deleteRemote {
		fmt.Println("ℹ️  Safe mode: Remote files will NOT be deleted (use --delete to enable)")
		fmt.Println("")
	} else {
		fmt.Println("⚠️  WARNING: Delete mode enabled - remote files not in local will be removed!")
		fmt.Println("")
	}

	// Create storage and syncer instances
	storage := api.BCDNStorage{
		ZoneName: zoneName,
		APIKey:   apiKey,
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

	// Run sync with syncPath
	fmt.Println("Starting sync...")
	err = syncerService.Sync(src, syncPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nSync failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nSync completed successfully!")
	os.Exit(0)
}
