package syncer

import (
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/veter2005/bunny-storage-sync/api"
)

// BCDNSyncer is the service that runs the synchronization operation
type BCDNSyncer struct {
	API         api.BCDNStorage
	DryRun      bool
	SizeOnly    bool    // Flag to compare files by size only
	OnlyMissing bool    // Flag to upload only missing files
}

// Sync synchronizes sourcePath with the BunnyCDN storage zone efficiently
func (s *BCDNSyncer) Sync(sourcePath string) error {
	objMap := make(map[string]api.BCDNObject)

	metrics := struct {
		total        int
		newFile      int
		modifiedFile int
		deletedFile  int
	}{0, 0, 0, 0}

	err := filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error accessing path %q: %v\n", path, err)
			return err
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}

		// If it's a directory, fetch the list of objects from the API
		if info.IsDir() {
			return s.fetchDirectory(objMap, relPath)
		}

		metrics.total += 1
		obj, exists := objMap[relPath]

		// 1. Check OnlyMissing flag: if file exists in storage, skip it
		if s.OnlyMissing && exists {
			log.Printf("DEBUG: %s exists, skipping (only-missing mode)\n", relPath)
			delete(objMap, relPath)
			return nil
		}

		shouldUpload := false
		var fileContent []byte
		var fsChecksum string

		// 2. Decide if upload is necessary
		if !exists {
			log.Printf("DEBUG: %s not found in storage, marking for upload\n", relPath)
			metrics.newFile += 1
			shouldUpload = true
		} else {
			if s.SizeOnly {
				// Compare by size only
				if int64(obj.Length) != info.Size() {
					log.Printf("DEBUG: %s size mismatch (Local: %d, Remote: %d), marking for upload\n", relPath, info.Size(), obj.Length)
					metrics.modifiedFile += 1
					shouldUpload = true
				} else {
					log.Printf("DEBUG: %s size matches, skipping\n", relPath)
				}
			} else {
				// Standard comparison by checksum
				var err error
				fileContent, fsChecksum, err = getFileContent(path)
				if err != nil {
					return err
				}

				if strings.ToLower(fsChecksum) != strings.ToLower(obj.Checksum) {
					log.Printf("DEBUG: %s checksum mismatch, marking for upload\n", relPath)
					metrics.modifiedFile += 1
					shouldUpload = true
				} else {
					log.Printf("DEBUG: %s matches checksum, skipping\n", relPath)
				}
			}
		}

		// 3. Perform upload if required
		if shouldUpload {
			if fileContent == nil {
				var err error
				fileContent, fsChecksum, err = getFileContent(path)
				if err != nil {
					return err
				}
			}
			err = s.uploadFile(relPath, fileContent, fsChecksum)
			if err != nil {
				return err
			}
		}

		// Remove from map to track files that exist only in storage
		delete(objMap, relPath)
		return nil
	})

	// Delete objects that remain in the map (exist in storage but not locally)
	for relPath, obj := range objMap {
		if !obj.IsDirectory {
			metrics.deletedFile += 1
			log.Printf("INFO: %s not found locally, deleting from storage\n", relPath)
			s.deletePath(relPath)
		}
	}

	log.Printf("Summary: Total files: %d, New: %d, Modified: %d, Deleted: %d\n", metrics.total, metrics.newFile, metrics.modifiedFile, metrics.deletedFile)
	return err
}

// fetchDirectory fetches path from BunnyCDN API & stores objects in a map
func (s *BCDNSyncer) fetchDirectory(objMap map[string]api.BCDNObject, path string) error {
	log.Printf("DEBUG: Fetching directory %s\n", path)
	objects, err := s.API.List(path)
	if err != nil {
		return err
	}
	zoneName := s.API.ZoneName
	for _, obj := range objects {
		objPath := strings.TrimPrefix(obj.Path+obj.ObjectName, "/"+zoneName+"/")
		objMap[objPath] = obj
	}
	return nil
}

func (s *BCDNSyncer) uploadFile(path string, content []byte, checksum string) error {
	log.Printf("Uploading file %s with checksum %s\n", path, checksum)
	if s.DryRun {
		return nil
	}
	return s.API.Upload(path, content, checksum)
}

func (s *BCDNSyncer) deletePath(path string) error {
	log.Printf("Deleting file %s\n", path)
	if s.DryRun {
		return nil
	}
	return s.API.Delete(path)
}

// getFileContent reads file from disk and calculates SHA256 checksum
func getFileContent(path string) ([]byte, string, error) {
	fileContent, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	checksum := sha256.Sum256(fileContent)
	return fileContent, fmt.Sprintf("%x", checksum), nil
}
