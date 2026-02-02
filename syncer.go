package syncer

import (
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mtyurt/bunnycdn-storage-sync/api"
)

type BCDNSyncer struct {
	API         api.BCDNStorage
	DryRun      bool
	SizeOnly    bool
	OnlyMissing bool
}

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
			log.Fatalf("error accessing path %q: %v\n", path, err)
			return err
		}

		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return s.fetchDirectory(objMap, relPath)
		}
		metrics.total += 1

		obj, ok := objMap[relPath]
		
		// 1. Если файла нет в облаке - загружаем
		if !ok {
			log.Printf("DEBUG: %s not found in storage, uploading...\n", relPath)
			metrics.newFile += 1
			content, checksum, err := getFileContent(path)
			if err != nil {
				return err
			}
			return s.uploadFile(relPath, content, checksum)
		}

		// 2. Если файл ЕСТЬ и включен --only-missing - пропускаем любые проверки
		if s.OnlyMissing {
			log.Printf("DEBUG: %s already exists, skipping (--only-missing)\n", relPath)
			delete(objMap, relPath)
			return nil
		}

		// 3. Проверка необходимости обновления
		needsUpdate := false
		if s.SizeOnly {
			if int64(obj.Length) != info.Size() {
				log.Printf("DEBUG: %s size mismatch (local: %d, remote: %d)\n", relPath, info.Size(), obj.Length)
				needsUpdate = true
			}
		} else {
			// Стандартная проверка по хешу
			_, fsChecksum, err := getFileContent(path)
			if err != nil {
				return err
			}
			if strings.ToLower(fsChecksum) != strings.ToLower(obj.Checksum) {
				log.Printf("DEBUG: %s checksum mismatch\n", relPath)
				needsUpdate = true
			}
		}

		if needsUpdate {
			metrics.modifiedFile += 1
			content, checksum, err := getFileContent(path)
			if err != nil {
				return err
			}
			delete(objMap, relPath)
			return s.uploadFile(relPath, content, checksum)
		}

		log.Printf("DEBUG: %s is up to date, skipping.\n", relPath)
		delete(objMap, relPath)
		return nil
	})

	// Удаление файлов, которых нет локально
	for relPath, obj := range objMap {
		if !obj.IsDirectory {
			metrics.deletedFile += 1
			s.deletePath(relPath)
		}
	}

	log.Printf("Total: %d, New: %d, Modified: %d, Deleted: %d\n", metrics.total, metrics.newFile, metrics.modifiedFile, metrics.deletedFile)
	return err
}

// ... остальные методы (fetchDirectory, uploadFile, deletePath, getFileContent) остаются без изменений ...

func (s *BCDNSyncer) fetchDirectory(objMap map[string]api.BCDNObject, path string) error {
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
	if s.DryRun {
		return nil
	}
	return s.API.Upload(path, content, checksum)
}

func (s *BCDNSyncer) deletePath(path string) error {
	log.Printf("Deleting %s\n", path)
	if s.DryRun {
		return nil
	}
	return s.API.Delete(path)
}

func getFileContent(path string) ([]byte, string, error) {
	fileContent, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	checksum := sha256.Sum256(fileContent)
	return fileContent, fmt.Sprintf("%x", checksum), nil
}
