package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	apiBase    = "https://araduneauctions.net"
	serverName = "Frostreaver" // hard-locked
)

type SalesLog struct {
	ID              int64   `json:"id"`
	ItemID          *int64  `json:"itemId"`
	Item            string  `json:"item"`
	Auctioneer      string  `json:"auctioneer"`
	TransactionType bool    `json:"transactionType"` // true = buy (WTB), false = sell (WTS)
	PlatPrice       float64 `json:"platPrice"`
	KronoPrice      float64 `json:"kronoPrice"`
	Datetime        string  `json:"datetime"`
}

type HistoryPoint struct {
	Datetime   string  `json:"datetime"`
	PlatPrice  float64 `json:"platPrice"`
	KronoPrice float64 `json:"kronoPrice"`
	IsBuy      bool    `json:"isBuy"`
	Auctioneer string  `json:"auctioneer"`
}

type PriceHistory struct {
	ItemID   int64          `json:"itemId"`
	ItemName string         `json:"itemName"`
	Points   []HistoryPoint `json:"points"`
}

type APIClient struct {
	http *http.Client
}

func NewAPIClient() *APIClient {
	return &APIClient{http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *APIClient) doJSON(req *http.Request, out interface{}) error {
	for attempt := 0; attempt < 3; attempt++ {
		req.Header.Set("User-Agent", "a_gnome_trader/1.0 (Frostreaver watch bot; personal non-commercial use)")
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := 5 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, perr := strconv.Atoi(ra); perr == nil && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			// A hostile or misconfigured Retry-After must not stall the poll
			// loop for hours; honor it up to a sane ceiling.
			if wait > time.Minute {
				wait = time.Minute
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			time.Sleep(wait)
			if req.GetBody != nil {
				if body, berr := req.GetBody(); berr == nil {
					req.Body = body
				}
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL)
		}
		err = json.NewDecoder(resp.Body).Decode(out)
		resp.Body.Close()
		return err
	}
	return fmt.Errorf("rate limited after retries: %s", req.URL)
}

func (c *APIClient) getJSON(u string, out interface{}) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

func (c *APIClient) postJSON(u string, body interface{}, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, out)
}

// CatalogEntry is one item in the server's full sales catalog.
type CatalogEntry struct {
	ItemID int64    `json:"itemId"`
	Name   string   `json:"name"`
	Price  *float64 `json:"price"`
}

// FetchCatalog returns every item with sales on the server. The API caches
// this response for an hour and documents it as fine to poll.
func (c *APIClient) FetchCatalog() ([]CatalogEntry, error) {
	u := fmt.Sprintf("%s/api/items/catalog?serverName=%s", apiBase, serverName)
	var out struct {
		Items []CatalogEntry `json:"items"`
	}
	if err := c.getJSON(u, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// BulkRecentSalesItem is the per-item slice of a bulk recent-sales response.
type BulkRecentSalesItem struct {
	ItemID int64      `json:"itemId"`
	Item   string     `json:"item"`
	Sales  []SalesLog `json:"sales"`
}

const (
	bulkMaxIDs       = 200 // request cap documented on POST /api/sales/bulk
	bulkPerItemLimit = 20  // documented max recent sales per item
)

// FetchBulkRecentSales returns the most recent WTS sales for up to
// bulkMaxIDs item ids in one request. This is the endpoint the API
// documents as the supported way to run a watchlist.
func (c *APIClient) FetchBulkRecentSales(itemIDs []int64) ([]BulkRecentSalesItem, error) {
	isBuy := false
	body := struct {
		ServerName   string  `json:"serverName"`
		ItemIDs      []int64 `json:"itemIds"`
		PerItemLimit int     `json:"perItemLimit"`
		IsBuy        *bool   `json:"isBuy"`
	}{ServerName: serverName, ItemIDs: itemIDs, PerItemLimit: bulkPerItemLimit, IsBuy: &isBuy}
	var out struct {
		Items []BulkRecentSalesItem `json:"items"`
	}
	if err := c.postJSON(apiBase+"/api/sales/bulk", body, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

type ItemSearchItem struct {
	ItemID   int64  `json:"itemId"`
	Item     string `json:"item"`
	HasSales bool   `json:"hasSales"`
}

// SearchItems resolves an item name to catalog entries with item ids.
func (c *APIClient) SearchItems(q string, limit int) ([]ItemSearchItem, error) {
	u := fmt.Sprintf("%s/api/items/search?q=%s&serverName=%s&limit=%d", apiBase, url.QueryEscape(q), serverName, limit)
	var out struct {
		Items []ItemSearchItem `json:"items"`
	}
	if err := c.getJSON(u, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// FetchHistory returns the full price history for an item on Frostreaver.
func (c *APIClient) FetchHistory(itemID int64) (*PriceHistory, error) {
	url := fmt.Sprintf("%s/api/items/%d/history/%s", apiBase, itemID, serverName)
	var out PriceHistory
	if err := c.getJSON(url, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type WindowAvg struct {
	AvgPlat float64
	Count   int
}

// SellAverages computes average priced sell listings over 1/3/7 day windows.
func SellAverages(points []HistoryPoint, now time.Time) (d1, d3, d7 WindowAvg) {
	type acc struct {
		sum float64
		n   int
	}
	var a1, a3, a7 acc
	for _, p := range points {
		if p.IsBuy || p.PlatPrice <= 0 {
			continue
		}
		t, err := time.Parse(time.RFC3339, p.Datetime)
		if err != nil {
			continue
		}
		age := now.Sub(t)
		if age < 0 {
			age = 0
		}
		if age <= 7*24*time.Hour {
			a7.sum += p.PlatPrice
			a7.n++
		}
		if age <= 3*24*time.Hour {
			a3.sum += p.PlatPrice
			a3.n++
		}
		if age <= 24*time.Hour {
			a1.sum += p.PlatPrice
			a1.n++
		}
	}
	mk := func(a acc) WindowAvg {
		if a.n == 0 {
			return WindowAvg{}
		}
		return WindowAvg{AvgPlat: a.sum / float64(a.n), Count: a.n}
	}
	return mk(a1), mk(a3), mk(a7)
}
