package extraction

import (
	"archive/zip"
	"encoding/xml"
	"image"
	"io"
	"strconv"
	"strings"
)

type xlsxDocument struct {
	sheets []string // one text entry per sheet
}

func openXLSX(filePath string) (*xlsxDocument, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	files := zipFiles(zr)

	// 1. shared strings
	shared := parseSharedStrings(files)

	// 2. workbook → sheet names & rIds
	type sheetRef struct {
		name string
		rID  string
	}
	var sheetRefs []sheetRef
	if rc := zipOpenPath(files, "xl/workbook.xml"); rc != nil {
		dec := xml.NewDecoder(rc)
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
				var name, rid string
				for _, a := range se.Attr {
					switch a.Name.Local {
					case "name":
						name = a.Value
					case "id":
						rid = a.Value
					}
				}
				if rid != "" {
					sheetRefs = append(sheetRefs, sheetRef{name, rid})
				}
			}
		}
		_ = rc.Close()
	}

	// 3. rels → rId to target path
	relsMap := parseOOXMLRels(files, "xl/_rels/workbook.xml.rels")

	// 4. parse each sheet
	var sheets []string
	for _, sr := range sheetRefs {
		target, ok := relsMap[sr.rID]
		if !ok {
			sheets = append(sheets, "")
			continue
		}
		sheetPath := "xl/" + target
		rc := zipOpenPath(files, sheetPath)
		if rc == nil {
			sheets = append(sheets, "")
			continue
		}
		text := parseSheet(rc, shared)
		_ = rc.Close()
		sheets = append(sheets, text)
	}

	return &xlsxDocument{sheets: sheets}, nil
}

func parseSharedStrings(files map[string]*zip.File) []string {
	rc := zipOpenPath(files, "xl/sharedStrings.xml")
	if rc == nil {
		return nil
	}
	defer rc.Close()

	var ss []string
	dec := xml.NewDecoder(rc)
	var inSI, inT bool
	var cur strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				ss = append(ss, cur.String())
				inSI = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
	}
	return ss
}

func parseSheet(r io.ReadCloser, shared []string) string {
	dec := xml.NewDecoder(r)
	var buf strings.Builder
	var cellType string
	var inV, inIS, inT bool
	var cellVal strings.Builder
	firstRow := true
	firstCell := true

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				if !firstRow {
					buf.WriteByte('\n')
				}
				firstRow = false
				firstCell = true
			case "c":
				cellType = ""
				cellVal.Reset()
				for _, a := range t.Attr {
					if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v":
				inV = true
			case "is":
				inIS = true
			case "t":
				if inIS {
					inT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "c":
				if !firstCell {
					buf.WriteByte('\t')
				}
				firstCell = false
				val := cellVal.String()
				if cellType == "s" {
					idx, err := strconv.Atoi(val)
					if err == nil && idx >= 0 && idx < len(shared) {
						buf.WriteString(shared[idx])
					}
				} else {
					buf.WriteString(val)
				}
			case "v":
				inV = false
			case "is":
				inIS = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inV {
				cellVal.Write(t)
			} else if inT && inIS {
				cellVal.Write(t)
			}
		}
	}
	return buf.String()
}

func (d *xlsxDocument) NumPage() int { return len(d.sheets) }
func (d *xlsxDocument) Text(i int) (string, error) {
	if i < 0 || i >= len(d.sheets) {
		return "", nil
	}
	return d.sheets[i], nil
}
func (d *xlsxDocument) Image(_ int) (image.Image, error) { return nil, ErrNotSupported }
func (d *xlsxDocument) Close() error                     { return nil }
