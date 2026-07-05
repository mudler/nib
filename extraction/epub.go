package extraction

import (
	"archive/zip"
	"encoding/xml"
	"image"
	"io"
	"path"
	"strings"
)

type epubDocument struct {
	chapters []string
}

func openEPUB(filePath string) (*epubDocument, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	files := zipFiles(zr)

	// 1. Parse container.xml → OPF path
	opfPath := findOPFPath(files)
	if opfPath == "" {
		return &epubDocument{}, nil
	}

	opfDir := path.Dir(opfPath)

	// 2. Parse OPF → manifest + spine
	manifest, spine := parseOPF(files, opfPath)

	// 3. Extract text from each spine item
	var chapters []string
	for _, idref := range spine {
		href, ok := manifest[idref]
		if !ok {
			continue
		}
		// Resolve href relative to OPF directory
		var itemPath string
		if opfDir == "." {
			itemPath = href
		} else {
			itemPath = opfDir + "/" + href
		}
		itemPath = path.Clean(itemPath)

		f, ok := files[itemPath]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		text := parseXHTMLText(rc)
		_ = rc.Close()
		if text != "" {
			chapters = append(chapters, text)
		}
	}

	return &epubDocument{chapters: chapters}, nil
}

func findOPFPath(files map[string]*zip.File) string {
	f, ok := files["META-INF/container.xml"]
	if !ok {
		return ""
	}
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "rootfile" {
			for _, a := range se.Attr {
				if a.Name.Local == "full-path" {
					return a.Value
				}
			}
		}
	}
	return ""
}

func parseOPF(files map[string]*zip.File, opfPath string) (manifest map[string]string, spine []string) {
	manifest = make(map[string]string)
	f, ok := files[opfPath]
	if !ok {
		return
	}
	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch se.Name.Local {
			case "item":
				var id, href string
				for _, a := range se.Attr {
					switch a.Name.Local {
					case "id":
						id = a.Value
					case "href":
						href = a.Value
					}
				}
				if id != "" && href != "" {
					manifest[id] = href
				}
			case "itemref":
				for _, a := range se.Attr {
					if a.Name.Local == "idref" {
						spine = append(spine, a.Value)
					}
				}
			}
		}
	}
	return
}

// parseXHTMLText extracts visible text from an XHTML document.
func parseXHTMLText(r io.ReadCloser) string {
	dec := xml.NewDecoder(r)
	// Tolerate HTML entities that aren't declared in the XML
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var buf strings.Builder
	depth := 0 // track nesting so we can detect block boundaries

	blockElements := map[string]bool{
		"p": true, "div": true, "br": true, "li": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"tr": true, "blockquote": true, "section": true, "article": true,
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if blockElements[t.Name.Local] && buf.Len() > 0 {
				buf.WriteByte('\n')
			}
		case xml.EndElement:
			depth--
			if blockElements[t.Name.Local] && buf.Len() > 0 {
				buf.WriteByte('\n')
			}
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text != "" {
				if buf.Len() > 0 {
					last := buf.String()[buf.Len()-1]
					if last != '\n' && last != ' ' {
						buf.WriteByte(' ')
					}
				}
				buf.WriteString(text)
			}
		}
	}
	_ = depth
	return strings.TrimSpace(buf.String())
}

func (d *epubDocument) NumPage() int { return len(d.chapters) }
func (d *epubDocument) Text(i int) (string, error) {
	if i < 0 || i >= len(d.chapters) {
		return "", nil
	}
	return d.chapters[i], nil
}
func (d *epubDocument) Image(_ int) (image.Image, error) { return nil, ErrNotSupported }
func (d *epubDocument) Close() error                     { return nil }
