package extraction

import (
	"errors"
	"fmt"
	"image"
	"log"
	"mime"
	"path/filepath"
	"strings"
)

var (
	// ErrNotSupported is returned when an operation (e.g. Image) is not
	// available for a given format.
	ErrNotSupported = errors.New("extraction: operation not supported for this format")

	// ErrUnsupportedFormat is returned by Open when the file extension is
	// not recognised.
	ErrUnsupportedFormat = errors.New("extraction: unsupported file format")
)

// Document provides page-oriented access to text and rendered images.
type Document interface {
	// NumPage returns the number of logical pages (sheets, slides, chapters).
	NumPage() int

	// Text returns the text content of page pageIndex (0-based).
	Text(pageIndex int) (string, error)

	// Image renders page pageIndex as an image.
	// Returns ErrNotSupported for formats that cannot render pages.
	Image(pageIndex int) (image.Image, error)

	// Close releases all resources held by the document.
	Close() error
}

// documentExtensions lists extensions supported by Open.
var documentExtensions = map[string]bool{
	".pdf": true, ".epub": true, ".docx": true, ".xlsx": true, ".pptx": true,
}

// IsDocumentFile returns true for document formats supported by the extraction package.
func IsDocumentFile(path string) bool {
	return documentExtensions[strings.ToLower(filepath.Ext(path))]
}

// IsPDFFile returns true if the file is a PDF.
func IsPDFFile(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

// ExtractText extracts all text from a document, concatenating every page.
// The result is sanitised with strings.ToValidUTF8.
func ExtractText(path string) (string, error) {
	doc, err := Open(path)
	if err != nil {
		return "", fmt.Errorf("extraction open failed: %w", err)
	}
	defer func() { _ = doc.Close() }()

	var buf strings.Builder
	for i := range doc.NumPage() {
		text, err := doc.Text(i)
		if err != nil {
			log.Printf("Warning: text extraction failed for page %d: %v", i, err)
			continue
		}
		buf.WriteString(text)
	}
	return strings.ToValidUTF8(buf.String(), " "), nil
}

// mimeToExt maps document MIME types to file extensions.
var mimeToExt = map[string]string{
	"application/pdf":      ".pdf",
	"application/epub+zip": ".epub",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
}

// IsDocumentContentType checks whether the given Content-Type header value
// corresponds to a supported document format. It returns the file extension
// and true if so, or ("", false) otherwise.
func IsDocumentContentType(contentType string) (ext string, ok bool) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	ext, ok = mimeToExt[mediaType]
	return
}

// Open opens a document at path and returns a Document whose concrete type
// is chosen by file extension.
func Open(path string) (Document, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return openPDF(path)
	case ".docx":
		return openDOCX(path)
	case ".xlsx":
		return openXLSX(path)
	case ".pptx":
		return openPPTX(path)
	case ".epub":
		return openEPUB(path)
	default:
		return nil, ErrUnsupportedFormat
	}
}
