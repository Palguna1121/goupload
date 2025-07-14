package goupload

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// UploadConfig holds configuration for image upload
type UploadConfig struct {
	MaxFileSize       int64    // in bytes
	AllowedExtensions []string // allowed file extensions
	StoragePath       string   // base storage path (e.g., "storage/public/images")
	BaseURL           string   // base URL for serving files (e.g., "http://localhost:8080")
	EnableTimestamp   bool     // add timestamp to filename
	CreateDateDir     bool     // create date-based directory structure
}

// UploadResult represents the result of an upload operation
type UploadResult struct {
	Success   bool     `json:"success"`
	Message   string   `json:"message"`
	FilePaths []string `json:"file_paths,omitempty"` // relative paths
	FileURLs  []string `json:"file_urls,omitempty"`  // full URLs
	Error     string   `json:"error,omitempty"`
}

// ImageUploader handles image upload operations
type ImageUploader struct {
	config UploadConfig
}

// NewImageUploader creates a new ImageUploader instance
func NewImageUploader(config UploadConfig) *ImageUploader {
	// Set default values
	if config.MaxFileSize == 0 {
		config.MaxFileSize = 10 << 20 // 10MB
	}
	if len(config.AllowedExtensions) == 0 {
		config.AllowedExtensions = []string{"jpg", "jpeg", "png", "webp", "gif", "bmp", "svg"}
	}
	if config.StoragePath == "" {
		config.StoragePath = "storage/app/public/uploads/images"
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:5220"
	}

	return &ImageUploader{config: config}
}

// HandleUpload handles HTTP upload request (Gin compatible)
func (u *ImageUploader) HandleUpload(c *gin.Context) {
	result := u.ProcessUpload(c)

	if result.Success {
		c.JSON(http.StatusOK, result)
	} else {
		c.JSON(http.StatusBadRequest, result)
	}
}

// ProcessUpload processes the upload without HTTP response (for custom handling)
func (u *ImageUploader) ProcessUpload(c *gin.Context) *UploadResult {
	// Get optional subdirectory from form
	subDir := c.DefaultPostForm("sub_dir", "")

	// Get optional custom max size
	maxSizeStr := c.DefaultPostForm("max_size", "")
	maxSize := u.config.MaxFileSize
	if maxSizeStr != "" {
		if customMax, err := u.parseSizeToBytes(maxSizeStr); err == nil && customMax > 0 {
			maxSize = customMax
		}
	}

	// Create target directory
	targetDir := u.buildTargetDirectory(subDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return &UploadResult{
			Success: false,
			Message: "Failed to create directory",
			Error:   err.Error(),
		}
	}

	// Get files from request
	files, err := u.getFilesFromRequest(c)
	if err != nil {
		return &UploadResult{
			Success: false,
			Message: "Failed to get files from request",
			Error:   err.Error(),
		}
	}

	if len(files) == 0 {
		return &UploadResult{
			Success: false,
			Message: "No files provided",
		}
	}

	var filePaths []string
	var fileURLs []string

	// Process each file
	for _, file := range files {
		// Validate file
		if err := u.validateFile(file, maxSize); err != nil {
			return &UploadResult{
				Success: false,
				Message: err.Error(),
			}
		}

		// Generate filename
		filename := u.generateFilename(file.Filename)
		savePath := filepath.Join(targetDir, filename)

		// Save file
		if err := c.SaveUploadedFile(file, savePath); err != nil {
			return &UploadResult{
				Success: false,
				Message: fmt.Sprintf("Failed to save file: %s", file.Filename),
				Error:   err.Error(),
			}
		}

		// Build paths for response
		relativePath := u.buildRelativePath(subDir, filename)
		fullURL := u.buildFullURL(relativePath)

		filePaths = append(filePaths, relativePath)
		fileURLs = append(fileURLs, fullURL)
	}

	return &UploadResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully uploaded %d file(s)", len(files)),
		FilePaths: filePaths,
		FileURLs:  fileURLs,
	}
}

// RegisterRoutes registers upload routes to Gin router
func (u *ImageUploader) RegisterRoutes(router *gin.Engine, basePath string) {
	if basePath == "" {
		basePath = "/upload"
	}

	router.POST(basePath, u.HandleUpload)
	router.POST(basePath+"/images", u.HandleUpload)
}

// RegisterRoutesWithGroup registers upload routes to Gin router group
func (u *ImageUploader) RegisterRoutesWithGroup(group *gin.RouterGroup, path string) {
	if path == "" {
		path = "/upload"
	}

	group.POST(path, u.HandleUpload)
	group.POST(path+"/images", u.HandleUpload)
}

// ServeStaticFiles registers static file serving route
func (u *ImageUploader) ServeStaticFiles(router *gin.Engine, routePath string) {
	if routePath == "" {
		routePath = "/storage"
	}

	// Remove trailing slash
	routePath = strings.TrimRight(routePath, "/")

	// Serve static files
	router.Static(routePath, u.config.StoragePath)
}

// Helper methods

func (u *ImageUploader) buildTargetDirectory(subDir string) string {
	basePath := u.config.StoragePath

	if u.config.CreateDateDir {
		dateDir := time.Now().Format("2006/01/02")
		basePath = filepath.Join(basePath, dateDir)
	}

	if subDir != "" {
		basePath = filepath.Join(basePath, filepath.Clean(subDir))
	}

	return basePath
}

func (u *ImageUploader) buildRelativePath(subDir, filename string) string {
	var parts []string

	if u.config.CreateDateDir {
		parts = append(parts, time.Now().Format("2006/01/02"))
	}

	if subDir != "" {
		parts = append(parts, subDir)
	}

	parts = append(parts, filename)

	return filepath.ToSlash(filepath.Join(parts...))
}

func (u *ImageUploader) buildFullURL(relativePath string) string {
	baseURL := strings.TrimRight(u.config.BaseURL, "/")
	return fmt.Sprintf("%s/storage/%s", baseURL, strings.TrimLeft(relativePath, "/"))
}

func (u *ImageUploader) getFilesFromRequest(c *gin.Context) ([]*multipart.FileHeader, error) {
	// Try multipart form first
	form, err := c.MultipartForm()
	if err == nil {
		if files, exists := form.File["files"]; exists && len(files) > 0 {
			return files, nil
		}
		if files, exists := form.File["images"]; exists && len(files) > 0 {
			return files, nil
		}
	}

	// Try single file
	if file, err := c.FormFile("file"); err == nil {
		return []*multipart.FileHeader{file}, nil
	}
	if file, err := c.FormFile("image"); err == nil {
		return []*multipart.FileHeader{file}, nil
	}

	return nil, fmt.Errorf("no files found in request")
}

func (u *ImageUploader) validateFile(file *multipart.FileHeader, maxSize int64) error {
	// Check file size
	if file.Size > maxSize {
		return fmt.Errorf("file %s exceeds maximum size of %s", file.Filename, u.formatBytes(maxSize))
	}

	// Check file extension
	if !u.isAllowedExtension(file.Filename) {
		return fmt.Errorf("file %s has disallowed extension. Allowed: %s",
			file.Filename, strings.Join(u.config.AllowedExtensions, ", "))
	}

	// Check MIME type
	mime, err := u.detectMimeType(file)
	if err != nil {
		return fmt.Errorf("failed to read MIME type for %s: %v", file.Filename, err)
	}

	if !u.isAllowedMimeType(mime) {
		return fmt.Errorf("file %s has disallowed MIME type: %s", file.Filename, mime)
	}

	return nil
}

func (u *ImageUploader) isAllowedExtension(filename string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	for _, allowed := range u.config.AllowedExtensions {
		if ext == strings.ToLower(allowed) {
			return true
		}
	}
	return false
}

func (u *ImageUploader) generateFilename(original string) string {
	ext := filepath.Ext(original)
	name := strings.TrimSuffix(original, ext)

	// Clean filename
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")

	if u.config.EnableTimestamp {
		timestamp := time.Now().Format("20060102_150405")
		return fmt.Sprintf("%s_%s%s", name, timestamp, ext)
	}

	return fmt.Sprintf("%s_%d%s", name, time.Now().Unix(), ext)
}

func (u *ImageUploader) parseSizeToBytes(input string) (int64, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	multiplier := int64(1)

	if strings.HasSuffix(input, "kb") {
		multiplier = 1 << 10
		input = strings.TrimSuffix(input, "kb")
	} else if strings.HasSuffix(input, "mb") {
		multiplier = 1 << 20
		input = strings.TrimSuffix(input, "mb")
	} else if strings.HasSuffix(input, "gb") {
		multiplier = 1 << 30
		input = strings.TrimSuffix(input, "gb")
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
	if err != nil {
		return 0, err
	}

	return int64(value * float64(multiplier)), nil
}

func (u *ImageUploader) formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (u *ImageUploader) detectMimeType(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	buffer := make([]byte, 512)
	_, err = f.Read(buffer)
	if err != nil {
		return "", err
	}

	return http.DetectContentType(buffer), nil
}

func (u *ImageUploader) isAllowedMimeType(mime string) bool {
	allowedMimes := []string{
		"image/jpeg",
		"image/png",
		"image/webp",
		"image/gif",
		"image/bmp",
		"image/svg+xml",
	}
	for _, allowed := range allowedMimes {
		if mime == allowed {
			return true
		}
	}
	return false
}
