package chunker

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Config controls the chunking behavior.
type Config struct {
	ChunkSizeChars    int  // default 2000
	OverlapChars      int  // default 200
	MaxChunks         int  // default 500
	PreserveParagraphs bool // default false (simplest for 6E-2)
}

// Chunk represents a single piece of text from the chunking operation.
type Chunk struct {
	Index         int    // 0-based position
	Content       string // chunk text
	ContentHash   string // SHA256 hex
	CharCount     int    // actual character count
	TokenEstimate int    // char_count / 4, rough estimate
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ChunkSizeChars:     2000,
		OverlapChars:       200,
		MaxChunks:          500,
		PreserveParagraphs: false,
	}
}

// ChunkText splits text into overlapping chunks.
// Returns nil for empty input. Silently caps at MaxChunks.
func ChunkText(text string, cfg Config) []Chunk {
	if text == "" || len(strings.TrimSpace(text)) == 0 {
		return nil
	}

	runes := []rune(text)
	chunks := make([]Chunk, 0, cfg.MaxChunks)
	step := cfg.ChunkSizeChars - cfg.OverlapChars
	if step <= 0 {
		step = 1
	}

	for start := 0; start < len(runes) && len(chunks) < cfg.MaxChunks; start += step {
		end := start + cfg.ChunkSizeChars
		if end > len(runes) {
			end = len(runes)
		}
		chunkText := strings.TrimSpace(string(runes[start:end]))
		if chunkText == "" {
			if end >= len(runes) {
				break
			}
			continue
		}
		h := sha256.Sum256([]byte(chunkText))
		chunks = append(chunks, Chunk{
			Index:         len(chunks),
			Content:       chunkText,
			ContentHash:   fmt.Sprintf("%x", h),
			CharCount:     len([]rune(chunkText)),
			TokenEstimate: len([]rune(chunkText)) / 4,
		})
		if end >= len(runes) {
			break
		}
	}
	return chunks
}
