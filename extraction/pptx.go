package extraction

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"strings"
)

type pptxDocument struct {
	slides []string
}

func openPPTX(filePath string) (*pptxDocument, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	files := zipFiles(zr)

	// Get slide order from presentation.xml → rels
	var slideRIDs []string
	if rc := zipOpenPath(files, "ppt/presentation.xml"); rc != nil {
		dec := xml.NewDecoder(rc)
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sldId" {
				for _, a := range se.Attr {
					// The r:id attribute resolves to a namespace ending in /relationships
					if a.Name.Local == "id" && a.Name.Space != "" &&
						strings.HasSuffix(a.Name.Space, "/relationships") {
						slideRIDs = append(slideRIDs, a.Value)
					}
				}
			}
		}
		_ = rc.Close()
	}

	relsMap := parseOOXMLRels(files, "ppt/_rels/presentation.xml.rels")

	var slides []string
	if len(slideRIDs) > 0 {
		for _, rid := range slideRIDs {
			target, ok := relsMap[rid]
			if !ok {
				slides = append(slides, "")
				continue
			}
			slidePath := "ppt/" + target
			rc := zipOpenPath(files, slidePath)
			if rc == nil {
				slides = append(slides, "")
				continue
			}
			text := parseSlideXML(rc)
			_ = rc.Close()
			slides = append(slides, text)
		}
	} else {
		// Fallback: read slides sequentially by name
		for i := 1; i <= 999; i++ {
			slidePath := fmt.Sprintf("ppt/slides/slide%d.xml", i)
			rc := zipOpenPath(files, slidePath)
			if rc == nil {
				break
			}
			text := parseSlideXML(rc)
			_ = rc.Close()
			slides = append(slides, text)
		}
	}

	return &pptxDocument{slides: slides}, nil
}

func parseSlideXML(r io.ReadCloser) string {
	dec := xml.NewDecoder(r)
	var buf strings.Builder
	var inText bool

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				buf.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
			}
		}
	}
	return buf.String()
}

func (d *pptxDocument) NumPage() int { return len(d.slides) }
func (d *pptxDocument) Text(i int) (string, error) {
	if i < 0 || i >= len(d.slides) {
		return "", nil
	}
	return d.slides[i], nil
}
func (d *pptxDocument) Image(_ int) (image.Image, error) { return nil, ErrNotSupported }
func (d *pptxDocument) Close() error                     { return nil }
