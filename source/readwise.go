package main

import (
	"errors"
	"fmt"
	"net/http"
)

type existingHighlight struct {
	ID           int64  `json:"id"`
	Text         string `json:"text"`
	Note         string `json:"note"`
	LocationType string `json:"location_type"`
}

func loadBookHighlights(client *http.Client, baseURL, token string, id int64) ([]existingHighlight, error) {
	var all []existingHighlight
	for page := 1; page <= 20; page++ {
		endpoint := fmt.Sprintf("%s/api/v2/highlights/?book_id=%d&page_size=1000&page=%d", baseURL, id, page)
		var response struct {
			Next    string              `json:"next"`
			Results []existingHighlight `json:"results"`
		}
		if err := sendReadwiseJSON(client, http.MethodGet, endpoint, token, nil, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Results...)
		if response.Next == "" {
			return all, nil
		}
	}
	return nil, errors.New("too many Readwise highlight pages for one book")
}
