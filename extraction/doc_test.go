package extraction

import (
	"strings"
	"testing"
)

func TestExtractTextPDF(t *testing.T) {
	got, err := ExtractText("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	// "PDF Form Example" is a stable substring present in the real sample.pdf
	// fixture ported from notetaker.
	if !strings.Contains(strings.ToLower(got), "pdf form example") {
		t.Fatalf("extracted text missing marker; got %q", got)
	}
}

func TestIsDocumentFile(t *testing.T) {
	if !IsDocumentFile("a.pdf") || !IsDocumentFile("b.docx") {
		t.Fatal("expected pdf/docx to be document files")
	}
	if IsDocumentFile("c.png") {
		t.Fatal("png must not be a document file")
	}
}
