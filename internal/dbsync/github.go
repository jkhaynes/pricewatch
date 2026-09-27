package dbsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// GitHub returns a Fetch that downloads path at ref from repo ("owner/name")
// through the GitHub contents API. The raw media type returns the file's bytes
// rather than JSON, for files up to 100 MB (DD-15).
func GitHub(client *http.Client, api, repo, ref, path, token string) func(context.Context, io.Writer) error {
	u := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", api, repo, path, url.QueryEscape(ref))
	return func(ctx context.Context, w io.Writer) error {
		if token == "" {
			return errors.New("no GitHub token: set PRICEWATCH_GITHUB_TOKEN")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github.raw")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("download %s: %w", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// The body is only there to make the message useful; failing to
			// read it changes nothing about the failure being reported.
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("download %s: HTTP %d: %s", path, resp.StatusCode, bytes.TrimSpace(msg))
		}
		if _, err := io.Copy(w, resp.Body); err != nil {
			return fmt.Errorf("download %s: %w", path, err)
		}
		return nil
	}
}
