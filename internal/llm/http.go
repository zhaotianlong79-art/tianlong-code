package llm

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// streamSSE performs req and invokes onData once per `data:` line of the
// resulting Server-Sent Events stream, until the stream ends or onData errors.
// The OpenAI `[DONE]` sentinel terminates the stream; Anthropic streams end
// naturally. Non-2xx responses are returned as errors with the body included.
func streamSSE(client *http.Client, req *http.Request, onData func(data []byte) error) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error (%d): %s", resp.StatusCode, string(raw))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Tool-call payloads can exceed the default 64KB line cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		if err := onData([]byte(data)); err != nil {
			return err
		}
	}
	return scanner.Err()
}
