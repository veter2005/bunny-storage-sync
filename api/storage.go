package api

import (
	"fmt"
	"log"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/levigross/grequests"
)

// BaseURL for BunnyCDN storage API
const BaseURL = "https://storage.bunnycdn.com"

// BCDNStorage contains storage & access information
type BCDNStorage struct {
	ZoneName string
	APIKey   string
}

// BCDNObject maps to BunnyCDN Storage API's response object
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

// BCDNTime is used to parse BCDNObject time
type BCDNTime struct {
	time.Time
}

// UnmarshalJSON uses 3 formats for datetime objects
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

// List returns BCDNObject list that exists under the path
func (s *BCDNStorage) List(path string) ([]BCDNObject, error) {
	url := fmt.Sprintf("%s/%s/%s/", BaseURL, s.ZoneName, path)
	log.Printf("DEBUG: Running List of %s\n", url)
	
	resp, err := grequests.Get(url, &grequests.RequestOptions{
		Headers: map[string]string{"AccessKey": s.APIKey},
	})
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}
	
	if !resp.Ok {
		return nil, fmt.Errorf("list failed with status %d: %s", resp.StatusCode, resp.String())
	}
	
	apiResponse := []BCDNObject{}
	err = resp.JSON(&apiResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	
	return apiResponse, nil
}

// Get fetches file from BCDN storage and returns the content.
func (s *BCDNStorage) Get(path string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	log.Printf("DEBUG: Running GET for %s\n", url)
	
	resp, err := grequests.Get(url, &grequests.RequestOptions{
		Headers: map[string]string{"AccessKey": s.APIKey},
	})
	if err != nil {
		return "", fmt.Errorf("get request failed: %w", err)
	}
	
	if !resp.Ok {
		return "", fmt.Errorf("get failed with status %d: %s", resp.StatusCode, resp.String())
	}
	
	log.Println("DEBUG:", resp.Header)
	return resp.String(), nil
}

// Upload uploads a file to BunnyCDN storage
func (s *BCDNStorage) Upload(path string, content []byte, checksum string) error {
	contentType := detectContentType(path)
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	log.Printf("DEBUG: Uploading %s/%s with checksum %s (Content-Type: %s)\n", 
		s.ZoneName, path, checksum, contentType)
	
	resp, err := grequests.Put(url, &grequests.RequestOptions{
		Headers: map[string]string{
			"AccessKey":    s.APIKey,
			"Accept":       "*/*",
			"Content-Type": contentType,
		},
		RequestBody: strings.NewReader(string(content)),
	})
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	
	if resp.Error != nil {
		return fmt.Errorf("upload error: %w", resp.Error)
	}
	
	if !resp.Ok {
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, resp.String())
	}
	
	return nil
}

// Delete path from BunnyCDN storage
func (s *BCDNStorage) Delete(path string) error {
	url := fmt.Sprintf("%s/%s/%s", BaseURL, s.ZoneName, path)
	log.Printf("DEBUG: Deleting %s/%s\n", s.ZoneName, path)
	
	resp, err := grequests.Delete(url, &grequests.RequestOptions{
		Headers: map[string]string{"AccessKey": s.APIKey},
	})
	if err != nil {
		return fmt.Errorf("delete request failed: %w", err)
	}
	
	if resp.Error != nil {
		return fmt.Errorf("delete error: %w", resp.Error)
	}
	
	if !resp.Ok {
		return fmt.Errorf("delete failed with status %d: %s", resp.StatusCode, resp.String())
	}
	
	return nil
}

// detectContentType detects the MIME type based on file extension
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
