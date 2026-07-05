package extraction

import (
	"fmt"
	"image"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

var (
	pdfiumPool pdfium.Pool
	poolOnce   sync.Once
	poolErr    error
)

func initPool() {
	poolOnce.Do(func() {
		pdfiumPool, poolErr = webassembly.Init(webassembly.Config{
			MinIdle:  1,
			MaxIdle:  1,
			MaxTotal: 4,
		})
	})
}

type pdfDocument struct {
	instance pdfium.Pdfium
	doc      references.FPDF_DOCUMENT
	pages    int
}

func openPDF(path string) (*pdfDocument, error) {
	initPool()
	if poolErr != nil {
		return nil, fmt.Errorf("pdfium init: %w", poolErr)
	}

	instance, err := pdfiumPool.GetInstance(30 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("pdfium instance: %w", err)
	}

	// #nosec G304 -- path is internally derived from the staged upload path
	data, err := os.ReadFile(path)
	if err != nil {
		_ = instance.Close()
		return nil, err
	}

	resp, err := instance.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("pdfium open: %w", err)
	}

	pageCount, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: resp.Document,
	})
	if err != nil {
		_, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: resp.Document})
		_ = instance.Close()
		return nil, fmt.Errorf("pdfium page count: %w", err)
	}

	return &pdfDocument{
		instance: instance,
		doc:      resp.Document,
		pages:    pageCount.PageCount,
	}, nil
}

func (d *pdfDocument) NumPage() int { return d.pages }

func (d *pdfDocument) Text(pageIndex int) (string, error) {
	resp, err := d.instance.GetPageText(&requests.GetPageText{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: d.doc,
				Index:    pageIndex,
			},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (d *pdfDocument) Image(pageIndex int) (image.Image, error) {
	resp, err := d.instance.RenderPageInDPI(&requests.RenderPageInDPI{
		DPI: 150,
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: d.doc,
				Index:    pageIndex,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if resp.Result.Image == nil {
		if resp.CleanupFunc != nil {
			resp.CleanupFunc()
		}
		return nil, fmt.Errorf("pdfium: nil image for page %d", pageIndex)
	}
	// Copy pixel data before cleanup frees the WASM-backed buffer.
	src := resp.Result.Image
	dst := image.NewRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	if resp.CleanupFunc != nil {
		resp.CleanupFunc()
	}
	return dst, nil
}

func (d *pdfDocument) Close() error {
	_, _ = d.instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: d.doc})
	_ = d.instance.Close()
	return nil
}
