package syncer

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/veter2005/bunny-storage-sync/api"
)

// BCDNSyncer is the service that runs the synchronization operation
type BCDNSyncer struct {
	API         api.BCDNStorage
	DryRun      bool
	SizeOnly    bool    // Flag to compare files by size only
	OnlyMissing bool    // Flag to upload only missing files
	Delete      bool    // Flag to delete remote files not present locally
	Concurrency int     // Number of concurrent upload/delete operations
	Verbose     bool    // Enable verbose logging
}

// operation represents a file operation to be performed
type operation struct {
	action   string // "upload"
	path     string // Full local path to read from
	relPath  string // Relative path for storage
	checksum string // SHA256 checksum
	isNew    bool   // Whether this is a new file
}

// syncMetrics tracks synchronization statistics
type syncMetrics struct {
	sync.Mutex
	total        int
	newFile      int
	modifiedFile int
	deletedFile  int
	skipped      int
	errors       int
}

// Sync synchronizes sourcePath with the BunnyCDN storage zone efficiently
// syncPath parameter allows syncing to a subdirectory in the zone (use "" for root)
func (s *BCDNSyncer) Sync(sourcePath string, syncPath string) error {
	// Validate source path
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("source path error: %w", err)
	}

	// Set default concurrency if not specified
	if s.Concurrency <= 0 {
		s.Concurrency = 5
	}

	// Normalize syncPath (remove leading/trailing slashes)
	syncPath = strings.Trim(syncPath, "/")

	// Fetch all remote objects first (only from syncPath prefix)
	s.logDebug("Fetching remote objects from path: %s", syncPath)
	objMap, err := s.fetchAllObjects(syncPath)
	if err != nil {
		return fmt.Errorf("failed to fetch remote objects: %w", err)
	}
	s.logDebug("Fetched %d remote objects", len(objMap))

	metrics := &syncMetrics{}

	// Collect all operations
	operations := []operation{}
	var opsLock sync.Mutex

	// Walk the filesystem and determine what needs to be done
	err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("ERROR: accessing path %q: %v\n", path, err)
			metrics.Lock()
			metrics.errors++
			metrics.Unlock()
			return nil // Continue walking despite errors
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Normalize path for cross-platform compatibility
		relPath = filepath.ToSlash(relPath)
		
		// Add syncPath prefix if specified
		if syncPath != "" {
			relPath = syncPath + "/" + relPath
		}

		metrics.Lock()
		metrics.total++
		metrics.Unlock()

		obj, exists := objMap[relPath]

		// Check OnlyMissing flag: if file exists in storage, skip it
		if s.OnlyMissing && exists {
			s.logDebug("%s exists, skipping (only-missing mode)", relPath)
			opsLock.Lock()
			delete(objMap, relPath)
			opsLock.Unlock()
			metrics.Lock()
			metrics.skipped++
			metrics.Unlock()
			return nil
		}

		shouldUpload := false
		var fileContent []byte
		var fsChecksum string

		// Decide if upload is necessary
		if !exists {
			s.logDebug("%s not found in storage, marking for upload", relPath)
			metrics.Lock()
			metrics.newFile++
			metrics.Unlock()
			shouldUpload = true
		} else {
			if s.SizeOnly {
				// Compare by size only
				if int64(obj.Length) != info.Size() {
					s.logDebug("%s size mismatch (Local: %d, Remote: %d), marking for upload", 
						relPath, info.Size(), obj.Length)
					metrics.Lock()
					metrics.modifiedFile++
					metrics.Unlock()
					shouldUpload = true
				} else {
					s.logDebug("%s size matches, skipping", relPath)
				}
			} else {
				// Standard comparison by checksum
				var err error
				fileContent, fsChecksum, err = getFileContent(path)
				if err != nil {
					log.Printf("ERROR: reading file %s: %v\n", relPath, err)
					metrics.Lock()
					metrics.errors++
					metrics.Unlock()
					return nil // Continue despite error
				}

				if strings.EqualFold(fsChecksum, obj.Checksum) {
					s.logDebug("%s matches checksum, skipping", relPath)
				} else {
					s.logDebug("%s checksum mismatch, marking for upload", relPath)
					metrics.Lock()
					metrics.modifiedFile++
					metrics.Unlock()
					shouldUpload = true
				}
			}
		}

		// Queue upload operation if needed
		if shouldUpload {
			// Read file if not already read (for checksum)
			if fileContent == nil && !s.SizeOnly {
				var err error
				fileContent, fsChecksum, err = getFileContent(path)
				if err != nil {
					log.Printf("ERROR: reading file %s: %v\n", relPath, err)
					metrics.Lock()
					metrics.errors++
					metrics.Unlock()
					return nil
				}
			}

			opsLock.Lock()
			operations = append(operations, operation{
				action:   "upload",
				path:     path, // Store the file path, not the content
				relPath:  relPath,
				checksum: fsChecksum,
				isNew:    !exists,
			})
			opsLock.Unlock()
		} else {
			metrics.Lock()
			metrics.skipped++
			metrics.Unlock()
		}

		// Remove from map to track files that exist only in storage
		opsLock.Lock()
		delete(objMap, relPath)
		opsLock.Unlock()

		return nil
	})

	if err != nil {
		return fmt.Errorf("filesystem walk failed: %w", err)
	}

	// Process uploads concurrently
	if len(operations) > 0 {
		s.logDebug("Processing %d upload operations with concurrency=%d", len(operations), s.Concurrency)
		if err := s.processOperationsConcurrently(operations, metrics); err != nil {
			return err
		}
	}

	// Delete objects that remain in the map (exist in storage but not locally)
	deleteOps := []string{}
	for relPath, obj := range objMap {
		if !obj.IsDirectory {
			deleteOps = append(deleteOps, relPath)
		}
	}

	if len(deleteOps) > 0 {
		if s.Delete {
			// Delete mode is enabled
			s.logDebug("Processing %d delete operations", len(deleteOps))
			metrics.Lock()
			metrics.deletedFile = len(deleteOps)
			metrics.Unlock()
			
			if err := s.processDeletesConcurrently(deleteOps, metrics); err != nil {
				return err
			}
		} else {
			// Delete mode is disabled - just log what would be deleted
			log.Printf("INFO: %d files exist remotely but not locally (use --delete to remove them):", len(deleteOps))
			for _, relPath := range deleteOps {
				log.Printf("  - %s", relPath)
			}
		}
	}

	// Print summary
	log.Printf("=== Sync Summary ===")
	log.Printf("Total files scanned: %d", metrics.total)
	log.Printf("New files uploaded: %d", metrics.newFile)
	log.Printf("Modified files updated: %d", metrics.modifiedFile)
	log.Printf("Files deleted: %d", metrics.deletedFile)
	log.Printf("Files skipped: %d", metrics.skipped)
	if metrics.errors > 0 {
		log.Printf("Errors encountered: %d", metrics.errors)
	}
	log.Printf("===================")

	if metrics.errors > 0 {
		return fmt.Errorf("sync completed with %d errors", metrics.errors)
	}

	return nil
}

// processOperationsConcurrently processes upload operations with controlled concurrency
func (s *BCDNSyncer) processOperationsConcurrently(operations []operation, metrics *syncMetrics) error {
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	var errLock sync.Mutex
	var errors []error

	for _, op := range operations {
		wg.Add(1)
		go func(op operation) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			// Read file content right before upload to minimize memory usage
			content, checksum, err := getFileContent(op.path)
			if err != nil {
				log.Printf("ERROR: failed to read file %s: %v", op.relPath, err)
				metrics.Lock()
				metrics.errors++
				metrics.Unlock()
				
				errLock.Lock()
				errors = append(errors, fmt.Errorf("read %s: %w", op.relPath, err))
				errLock.Unlock()
				return
			}

			err = s.uploadFile(op.relPath, content, checksum)
			if err != nil {
				log.Printf("ERROR: upload failed for %s: %v", op.relPath, err)
				metrics.Lock()
				metrics.errors++
				metrics.Unlock()

				errLock.Lock()
				errors = append(errors, fmt.Errorf("upload %s: %w", op.relPath, err))
				errLock.Unlock()
			}
		}(op)
	}

	wg.Wait()
	
	if len(errors) > 0 {
		// Return summary of all errors
		return fmt.Errorf("%d upload errors occurred (first: %v)", len(errors), errors[0])
	}
	
	return nil
}

// processDeletesConcurrently processes delete operations with controlled concurrency
func (s *BCDNSyncer) processDeletesConcurrently(deleteOps []string, metrics *syncMetrics) error {
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	var errLock sync.Mutex
	var errors []error

	for _, relPath := range deleteOps {
		wg.Add(1)
		go func(relPath string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			log.Printf("INFO: %s not found locally, deleting from storage", relPath)
			err := s.deletePath(relPath)
			if err != nil {
				log.Printf("ERROR: delete failed for %s: %v", relPath, err)
				metrics.Lock()
				metrics.errors++
				metrics.Unlock()

				errLock.Lock()
				errors = append(errors, fmt.Errorf("delete %s: %w", relPath, err))
				errLock.Unlock()
			}
		}(relPath)
	}

	wg.Wait()
	
	if len(errors) > 0 {
		// Return summary of all errors
		return fmt.Errorf("%d delete errors occurred (first: %v)", len(errors), errors[0])
	}
	
	return nil
}

// fetchAllObjects recursively fetches all objects from the storage zone starting from prefix
func (s *BCDNSyncer) fetchAllObjects(prefix string) (map[string]api.BCDNObject, error) {
	objMap := make(map[string]api.BCDNObject)
	
	// Use a queue to handle recursive directory fetching
	type dirToFetch struct {
		path string
	}
	
	queue := []dirToFetch{{path: prefix}}
	processed := make(map[string]bool)
	
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		
		// Skip if already processed
		if processed[current.path] {
			continue
		}
		processed[current.path] = true
		
		s.logDebug("Fetching directory: %s", current.path)
		objects, err := s.API.List(current.path)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", current.path, err)
		}
		
		zoneName := s.API.ZoneName
		for _, obj := range objects {
			// Construct the object path
			fullPath := obj.Path
			if !strings.HasSuffix(fullPath, "/") && fullPath != "" {
				fullPath += "/"
			}
			fullPath += obj.ObjectName
			
			// Remove zone name prefix and leading slash
			objPath := strings.TrimPrefix(fullPath, "/"+zoneName+"/")
			objPath = strings.TrimPrefix(objPath, zoneName+"/")
			objPath = strings.TrimPrefix(objPath, "/")
			
			// Normalize path
			objPath = filepath.ToSlash(filepath.Clean(objPath))
			
			if obj.IsDirectory {
				// Queue subdirectory for fetching
				queue = append(queue, dirToFetch{path: objPath})
			} else {
				// Add file to map
				objMap[objPath] = obj
			}
		}
	}
	
	return objMap, nil
}

func (s *BCDNSyncer) uploadFile(path string, content []byte, checksum string) error {
	log.Printf("Uploading file %s (size: %d bytes, checksum: %s)", path, len(content), checksum)
	if s.DryRun {
		log.Printf("DRY-RUN: Would upload %s", path)
		return nil
	}
	return s.API.Upload(path, content, checksum)
}

func (s *BCDNSyncer) deletePath(path string) error {
	log.Printf("Deleting file %s", path)
	if s.DryRun {
		log.Printf("DRY-RUN: Would delete %s", path)
		return nil
	}
	return s.API.Delete(path)
}

func (s *BCDNSyncer) logDebug(format string, args ...interface{}) {
	if s.Verbose {
		log.Printf("DEBUG: "+format, args...)
	}
}

// getFileContent reads file from disk and calculates SHA256 checksum
func getFileContent(path string) ([]byte, string, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read file: %w", err)
	}
	checksum := sha256.Sum256(fileContent)
	return fileContent, fmt.Sprintf("%x", checksum), nil
}
