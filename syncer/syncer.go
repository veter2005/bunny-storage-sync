package syncer

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/veter2005/bunny-storage-sync/api"
)

type BCDNSyncer struct {
	API         api.BCDNStorage
	DryRun      bool
	SizeOnly    bool
	OnlyMissing bool
	Delete      bool
	Concurrency int
	Verbose     bool
}

type operation struct {
	action   string
	path     string
	relPath  string
	checksum string
	isNew    bool
}

type syncMetrics struct {
	sync.Mutex
	total        int
	newFile      int
	modifiedFile int
	deletedFile  int
	skipped      int
	errors       int
}

func (s *BCDNSyncer) Sync(sourcePath string, syncPath string) error {
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("source path error: %w", err)
	}

	if s.Concurrency <= 0 {
		s.Concurrency = 5
	}

	syncPath = strings.Trim(syncPath, "/")

	fmt.Println("Fetching remote objects (parallel scan)...")
	objMap, err := s.fetchAllObjectsParallel(syncPath)
	if err != nil {
		return fmt.Errorf("failed to fetch remote objects: %w", err)
	}
	log.Printf("Fetched %d remote objects", len(objMap))

	metrics := &syncMetrics{}
	operations := []operation{}
	var opsLock sync.Mutex

	err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("ERROR: accessing path %q: %v\n", path, err)
			metrics.Lock()
			metrics.errors++
			metrics.Unlock()
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(sourcePath, path)
		relPath = filepath.ToSlash(relPath)
		if syncPath != "" {
			relPath = syncPath + "/" + relPath
		}

		metrics.Lock()
		metrics.total++
		metrics.Unlock()

		obj, exists := objMap[relPath]

		if s.OnlyMissing && exists {
			opsLock.Lock()
			delete(objMap, relPath)
			opsLock.Unlock()
			metrics.Lock()
			metrics.skipped++
			metrics.Unlock()
			return nil
		}

		shouldUpload := false
		var fsChecksum string

		if !exists {
			metrics.Lock()
			metrics.newFile++
			metrics.Unlock()
			shouldUpload = true
		} else {
			if s.SizeOnly {
				if int64(obj.Length) != info.Size() {
					metrics.Lock()
					metrics.modifiedFile++
					metrics.Unlock()
					shouldUpload = true
				}
			} else {
				_, fsChecksum, err = getFileContent(path)
				if err != nil {
					log.Printf("ERROR: reading file %s: %v\n", relPath, err)
					metrics.Lock()
					metrics.errors++
					metrics.Unlock()
					return nil
				}
				if !strings.EqualFold(fsChecksum, obj.Checksum) {
					metrics.Lock()
					metrics.modifiedFile++
					metrics.Unlock()
					shouldUpload = true
				}
			}
		}

		if shouldUpload {
			opsLock.Lock()
			operations = append(operations, operation{
				action:   "upload",
				path:     path,
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

		opsLock.Lock()
		delete(objMap, relPath)
		opsLock.Unlock()

		return nil
	})

	if err != nil {
		return fmt.Errorf("filesystem walk failed: %w", err)
	}

	if len(operations) > 0 {
		if err := s.processOperationsConcurrently(operations, metrics); err != nil {
			return err
		}
	}

	if s.Delete && len(objMap) > 0 {
		deleteOps := []string{}
		for p, o := range objMap {
			if !o.IsDirectory {
				deleteOps = append(deleteOps, p)
			}
		}
		if len(deleteOps) > 0 {
			metrics.Lock()
			metrics.deletedFile = len(deleteOps)
			metrics.Unlock()
			s.processDeletesConcurrently(deleteOps, metrics)
		}
	}

	s.printSummary(metrics)
	return nil
}

func (s *BCDNSyncer) fetchAllObjectsParallel(rootPrefix string) (map[string]api.BCDNObject, error) {
	objMap := make(map[string]api.BCDNObject)
	var mapLock sync.Mutex

	dirQueue := make(chan string, 100000)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var fetchErr error

	wg.Add(1)
	dirQueue <- rootPrefix

	for i := 0; i < s.Concurrency; i++ {
		go func() {
			for path := range dirQueue {
				objects, err := s.API.List(path)
				if err != nil {
					errOnce.Do(func() { fetchErr = err })
					wg.Done()
					continue
				}

				for _, obj := range objects {
					fullPath := obj.Path
					if !strings.HasSuffix(fullPath, "/") && fullPath != "" {
						fullPath += "/"
					}
					fullPath += obj.ObjectName
					
					objPath := strings.TrimPrefix(fullPath, "/"+s.API.ZoneName+"/")
					objPath = strings.TrimPrefix(objPath, s.API.ZoneName+"/")
					objPath = strings.TrimPrefix(objPath, "/")
					objPath = filepath.ToSlash(filepath.Clean(objPath))

					if obj.IsDirectory {
						wg.Add(1)
						go func(p string) {
							dirQueue <- p
						}(objPath)
					} else {
						mapLock.Lock()
						objMap[objPath] = obj
						mapLock.Unlock()
					}
				}
				wg.Done()
			}
		}()
	}

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(dirQueue)
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(15 * time.Minute):
		return nil, fmt.Errorf("listing timeout: possible network issue or massive directory structure")
	}

	return objMap, fetchErr
}

func (s *BCDNSyncer) processOperationsConcurrently(operations []operation, metrics *syncMetrics) error {
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	for _, op := range operations {
		wg.Add(1)
		go func(o operation) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			content, checksum, err := getFileContent(o.path)
			if err != nil {
				metrics.Lock()
				metrics.errors++
				metrics.Unlock()
				return
			}

			if !s.DryRun {
				if err := s.API.Upload(o.relPath, content, checksum); err != nil {
					log.Printf("ERROR: upload failed for %s: %v", o.relPath, err)
					metrics.Lock()
					metrics.errors++
					metrics.Unlock()
				}
			} else {
				log.Printf("DRY-RUN: Would upload %s", o.relPath)
			}
		}(op)
	}
	wg.Wait()
	return nil
}

func (s *BCDNSyncer) processDeletesConcurrently(deleteOps []string, metrics *syncMetrics) {
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	for _, path := range deleteOps {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !s.DryRun {
				log.Printf("Deleting %s", p)
				s.API.Delete(p)
			} else {
				log.Printf("DRY-RUN: Would delete %s", p)
			}
		}(path)
	}
	wg.Wait()
}

func (s *BCDNSyncer) printSummary(m *syncMetrics) {
	log.Printf("=== Sync Summary ===")
	log.Printf("Total: %d, New: %d, Updated: %d, Deleted: %d, Errors: %d", 
		m.total, m.newFile, m.modifiedFile, m.deletedFile, m.errors)
}

func (s *BCDNSyncer) logDebug(format string, args ...interface{}) {
	if s.Verbose {
		log.Printf("DEBUG: "+format, args...)
	}
}

func getFileContent(path string) ([]byte, string, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	checksum := sha256.Sum256(fileContent)
	return fileContent, fmt.Sprintf("%x", checksum), nil
}
