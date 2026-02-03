package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const BaseURL = "https://storage.bunnycdn.com"

type BCDNStorage struct {
	ZoneName string
	APIKey   string
	Verbose  bool
}

type BCDNObject struct {
	GUID            string   `json:"Guid"`
	StorageZoneName string   `json:"StorageZoneName"`
	Path            string   `json:"Path"`
	ObjectName      string   `json:"ObjectName"`
	Length          int      `json:"Length"`
	LastChanged     BCDNTime `json:"LastChanged"`
	ServerID        int      `json:"ServerId"`
	IsDirectory     bool     `json:"IsDirectory"`
	UserID          string   `json:"UserId"`
	DateCreated     BCDNTime `json:"DateCreated"`
	StorageZoneID   int      `json:"StorageZoneId"`
	Checksum        string   `json:"Checksum"`
	ReplicatedZones string   `json:"ReplicatedZones"`
}

type BCDNTime struct {
	time.Time
}

func (t *BCDNTime) UnmarshalJSON(buf []byte) error {
	trimmed := strings.Trim(string(buf), `"`)
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.0",
		"2006-01-02T15:04:05.00",
		"2006-01-02T15:04:05.000",
	}
	var latestError error
	for _, format := range formats {
		tt, err := time.Parse(format, trimmed)
		if err == nil {
			t.Time = tt
			return nil
		}
		latestError = err
	}
	return latestError
}

func (s *BCDNStorage) logDebug(format string, args ...interface{}) {
	if s.Verbose {
		log.Printf("DEBUG: [API] "+format, args...)
	}
}

func (s *BCDNStorage) List(path string) ([]BCDNObject, error) {
	url := fmt.Sprintf("%s/%s/%s/", BaseURL, s.ZoneName, path)
	s.logDebug("Listing directory: %s", path)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("AccessKey", s.APIKey)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	var apiResponse []BCDNObject
	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	
	return apiResponse, nil
}

func (s *BCDNStorage) Get(path string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	s.logDebug("Running GET for %s", url)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("AccessKey", s.APIKey)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	
	return string(body), nil
}

func (s *BCDNStorage) Upload(path string, content []byte, checksum string) error {
	contentType := detectContentType(path)
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	s.logDebug("Uploading %s/%s (Type: %s)", s.ZoneName, path, contentType)
	
	req, err := http.NewRequest("PUT", url, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("AccessKey", s.APIKey)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", contentType)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

func (s *BCDNStorage) Delete(path string) error {
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	s.logDebug("Deleting %s/%s", s.ZoneName, path)
	
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("AccessKey", s.APIKey)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delete request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

func detectContentType(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "application/octet-stream"
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}
