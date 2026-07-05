package extraction

import (
	"archive/zip"
	"encoding/xml"
	"image"
	"io"
	"strings"
)

type docxDocument struct {
	text string
}

func openDOCX(path string) (*docxDocument, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			text, err := parseDOCXBody(rc)
			_ = rc.Close()
			if err != nil {
				return nil, err
			}
			return &docxDocument{text: text}, nil
		}
	}
	return &docxDocument{}, nil
}

// parseDOCXBody extracts text from the word/document.xml stream.
func parseDOCXBody(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var buf strings.Builder
	var inText bool

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf.String(), nil // return what we have
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			if local == "t" {
				inText = true
			}
		case xml.EndElement:
			local := t.Name.Local
			switch local {
			case "t":
				inText = false
			case "p":
				buf.WriteByte('\n')
			case "br":
				buf.WriteByte('\n')
			case "tab":
				buf.WriteByte('\t')
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
			}
		}
	}
	return buf.String(), nil
}

func (d *docxDocument) NumPage() int                     { return 1 }
func (d *docxDocument) Text(_ int) (string, error)       { return d.text, nil }
func (d *docxDocument) Image(_ int) (image.Image, error) { return nil, ErrNotSupported }
func (d *docxDocument) Close() error                     { return nil }
