package processors

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dslipak/pdf"
	"github.com/gogits/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// FileProcessor handles processing of various file types
type FileProcessor struct{}

// NewFileProcessor creates a new file processor instance
func NewFileProcessor() *FileProcessor {
	return &FileProcessor{}
}

// ProcessFile processes a file based on its content type and extracts text
func (fp *FileProcessor) ProcessFile(data []byte, contentType, filename string) (string, bool, error) {
	shouldProcessURLs := fp.shouldProcessURLs(contentType, filename)

	switch {
	case strings.HasPrefix(contentType, "application/pdf"):
		text, err := fp.processPDF(data)
		return text, shouldProcessURLs, err
	case strings.HasPrefix(contentType, "text/"):
		text, err := fp.processTextFile(data)
		return text, shouldProcessURLs, err
	case fp.isTextFileByExtension(filename):
		// For files that might be text but don't have proper content type
		text, err := fp.processTextFile(data)
		return text, shouldProcessURLs, err
	default:
		return "", false, fmt.Errorf("unsupported file type: %s", contentType)
	}
}

// processPDF extracts text from PDF files
func (fp *FileProcessor) processPDF(data []byte) (string, error) {
	// Create a reader from the PDF data
	reader := bytes.NewReader(data)

	// Open the PDF
	pdfReader, err := pdf.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}

	// Extract plain text from all pages
	textReader, err := pdfReader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("failed to extract text from PDF: %w", err)
	}

	// Read all text content
	var textBuffer bytes.Buffer
	_, err = textBuffer.ReadFrom(textReader)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF text: %w", err)
	}

	text := textBuffer.String()

	// Clean up the extracted text
	text = fp.cleanExtractedText(text)

	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("no text content found in PDF")
	}

	return text, nil
}

// shouldProcessURLs determines if URLs should be processed for a given file type.
func (fp *FileProcessor) shouldProcessURLs(contentType string, filename string) bool {
	// By default, do not process URLs in text or PDF files
	if strings.HasPrefix(contentType, "application/pdf") {
		return false
	}

	// Check for common text file extensions
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".log", ".csv", ".tsv":
		return false
	}

	// For generic text content types, default to false unless specified otherwise
	if strings.HasPrefix(contentType, "text/plain") {
		return false
	}

	// For other file types, assume URLs should be processed
	return true
}

// processTextFile processes text files with encoding detection
func (fp *FileProcessor) processTextFile(data []byte) (string, error) {
	// First, try to detect if it's already valid UTF-8
	if isValidUTF8(data) {
		return string(data), nil
	}

	// Use chardet to detect the encoding
	detector := chardet.NewTextDetector()
	result, err := detector.DetectBest(data)
	if err != nil {
		// Fallback to raw string if detection fails
		return string(data), nil
	}

	// Get the appropriate decoder for the detected encoding
	decoder := fp.getDecoderForEncoding(result.Charset)
	if decoder == nil {
		// Fallback to raw string if no decoder found
		return string(data), nil
	}

	// Decode the text
	decoded, err := decoder.Bytes(data)
	if err != nil {
		// Fallback to raw string if decoding fails
		return string(data), nil
	}

	return string(decoded), nil
}

// getDecoderForEncoding returns the appropriate decoder for the given encoding
func (fp *FileProcessor) getDecoderForEncoding(charset string) *encoding.Decoder {
	charset = strings.ToLower(charset)

	switch {
	// Unicode encodings
	case strings.Contains(charset, "utf-8"):
		return nil // Already UTF-8
	case strings.Contains(charset, "utf-16"):
		if strings.Contains(charset, "be") {
			return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
		}
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	// Note: UTF-32 support may not be available in all Go versions
	// case strings.Contains(charset, "utf-32"):
	//	if strings.Contains(charset, "be") {
	//		return unicode.UTF32(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
	//	}
	//	return unicode.UTF32(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()

	// Western European encodings
	case strings.Contains(charset, "iso-8859-1") || strings.Contains(charset, "latin-1"):
		return charmap.ISO8859_1.NewDecoder()
	case strings.Contains(charset, "iso-8859-2"):
		return charmap.ISO8859_2.NewDecoder()
	case strings.Contains(charset, "iso-8859-15"):
		return charmap.ISO8859_15.NewDecoder()
	case strings.Contains(charset, "windows-1252") || strings.Contains(charset, "cp1252"):
		return charmap.Windows1252.NewDecoder()
	case strings.Contains(charset, "windows-1251") || strings.Contains(charset, "cp1251"):
		return charmap.Windows1251.NewDecoder()

	// Japanese encodings
	case strings.Contains(charset, "shift_jis") || strings.Contains(charset, "sjis"):
		return japanese.ShiftJIS.NewDecoder()
	case strings.Contains(charset, "euc-jp"):
		return japanese.EUCJP.NewDecoder()
	case strings.Contains(charset, "iso-2022-jp"):
		return japanese.ISO2022JP.NewDecoder()

	// Chinese encodings
	case strings.Contains(charset, "gb2312") || strings.Contains(charset, "gb18030"):
		return simplifiedchinese.GBK.NewDecoder()
	case strings.Contains(charset, "big5"):
		return traditionalchinese.Big5.NewDecoder()

	// Korean encodings
	case strings.Contains(charset, "euc-kr"):
		return korean.EUCKR.NewDecoder()

	default:
		return nil
	}
}

// isTextFileByExtension checks if a file is likely a text file based on its extension
func (fp *FileProcessor) isTextFileByExtension(filename string) bool {
	filename = strings.ToLower(filename)

	textExtensions := []string{
		".txt", ".text", ".log", ".md", ".markdown", ".rst",
		".c", ".cpp", ".cc", ".cxx", ".h", ".hpp", ".hxx",
		".go", ".py", ".js", ".ts", ".java", ".kt", ".scala",
		".rb", ".php", ".pl", ".sh", ".bash", ".zsh", ".fish",
		".html", ".htm", ".xml", ".css", ".scss", ".sass", ".less",
		".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf",
		".sql", ".r", ".m", ".swift", ".rs", ".dart", ".lua",
		".vim", ".emacs", ".el", ".tex", ".bib", ".csv", ".tsv",
		".dockerfile", ".makefile", ".cmake", ".gitignore",
		".editorconfig", ".clang-format", ".prettierrc",
	}

	for _, ext := range textExtensions {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}

	// Special cases for files without extensions
	baseName := strings.ToLower(filename)
	specialNames := []string{
		"readme", "license", "changelog", "todo", "makefile",
		"dockerfile", "jenkinsfile", "vagrantfile", "gemfile",
		"rakefile", "gulpfile", "gruntfile", "webpack",
	}

	for _, name := range specialNames {
		if baseName == name || strings.HasPrefix(baseName, name+".") {
			return true
		}
	}

	return false
}

// cleanExtractedText cleans up extracted text content
func (fp *FileProcessor) cleanExtractedText(text string) string {
	// Remove excessive whitespace and normalize line endings
	lines := strings.Split(text, "\n")
	var cleanedLines []string

	for _, line := range lines {
		// Trim whitespace from each line
		line = strings.TrimSpace(line)

		// Skip empty lines that are just whitespace
		if line != "" {
			cleanedLines = append(cleanedLines, line)
		}
	}

	// Join lines back together with single newlines
	cleaned := strings.Join(cleanedLines, "\n")

	// Remove any remaining excessive whitespace
	cleaned = strings.TrimSpace(cleaned)

	return cleaned
}

// isValidUTF8 checks if the data is valid UTF-8
func isValidUTF8(data []byte) bool {
	return strings.ToValidUTF8(string(data), "�") == string(data)
}

// GetSupportedFileTypes returns a list of supported file types for documentation
func (fp *FileProcessor) GetSupportedFileTypes() []string {
	return []string{
		"PDF files (.pdf)",
		"Text files (.txt, .md, .log, etc.)",
		"Source code files (.go, .py, .js, .java, .c, .cpp, etc.)",
		"Configuration files (.json, .yaml, .xml, .ini, etc.)",
		"Documentation files (README, LICENSE, etc.)",
		"And many more text-based formats",
	}
}

// GetProcessingInfo returns information about file processing capabilities
func (fp *FileProcessor) GetProcessingInfo() string {
	return `**File Processing Capabilities:**

📄 **PDF Files:**
- Extracts plain text from PDF documents
- Handles multi-page documents
- Preserves text structure where possible

📝 **Text Files:**
- Auto-detects character encoding (UTF-8, UTF-16, Latin-1, etc.)
- Supports international text (Chinese, Japanese, Korean, etc.)
- Handles various programming languages and formats
- Processes configuration files, documentation, logs, etc.

✅ **Supported Formats:**
- PDF documents (.pdf)
- Plain text (.txt, .md, .log)
- Source code (.go, .py, .js, .java, .c, .cpp, .rs, etc.)
- Config files (.json, .yaml, .xml, .ini, .toml)
- Documentation (README, LICENSE, CHANGELOG)
- And many more text-based formats`
}
