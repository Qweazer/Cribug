package chunker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkEmpty(t *testing.T) {
	cfg := DefaultConfig()
	chunks := ChunkText("", cfg)
	require.Nil(t, chunks)
	require.Equal(t, 0, len(chunks))
}

func TestChunkWhitespaceOnly(t *testing.T) {
	cfg := DefaultConfig()
	chunks := ChunkText("   \n\t  \n  ", cfg)
	require.Nil(t, chunks)
	require.Equal(t, 0, len(chunks))
}

func TestChunkSmallText(t *testing.T) {
	cfg := DefaultConfig()
	text := "Hello, world!"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 1)
	require.Equal(t, 0, chunks[0].Index)
	require.Equal(t, strings.TrimSpace(text), chunks[0].Content)
	require.NotEmpty(t, chunks[0].ContentHash)
	require.Equal(t, len([]rune(strings.TrimSpace(text))), chunks[0].CharCount)
	require.Equal(t, chunks[0].CharCount/4, chunks[0].TokenEstimate)
}

func TestChunkExactSize(t *testing.T) {
	cfg := Config{
		ChunkSizeChars: 10,
		OverlapChars:   0,
		MaxChunks:      500,
	}
	text := "0123456789"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 1)
	require.Equal(t, text, chunks[0].Content)
	require.Equal(t, 10, chunks[0].CharCount)
}

func TestChunkMultipleChunks(t *testing.T) {
	cfg := Config{
		ChunkSizeChars: 10,
		OverlapChars:   0,
		MaxChunks:      500,
	}
	// "0123456789" + "abcdefghij" = 20 chars -> 2 chunks
	text := "0123456789abcdefghij"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 2)
	require.Equal(t, "0123456789", chunks[0].Content)
	require.Equal(t, "abcdefghij", chunks[1].Content)
	require.Equal(t, 0, chunks[0].Index)
	require.Equal(t, 1, chunks[1].Index)
}

func TestChunkOverlap(t *testing.T) {
	cfg := Config{
		ChunkSizeChars: 10,
		OverlapChars:   5,
		MaxChunks:      500,
	}
	// text: "0123456789abcdef"
	// chunk 0: "0123456789"
	// chunk 1: "56789abcde"  (start at 5, end at 15)
	// chunk 2: "abcdef"      (start at 10, end at 16)
	text := "0123456789abcdef"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 3)
	require.Equal(t, "0123456789", chunks[0].Content)
	require.Equal(t, "56789abcde", chunks[1].Content)
	require.Equal(t, "abcdef", chunks[2].Content)
	// Verify overlap: chunk0 suffix matches chunk1 prefix
	require.True(t, strings.HasSuffix(chunks[0].Content, "56789"))
	require.True(t, strings.HasPrefix(chunks[1].Content, "56789"))
}

func TestChunkMaxChunks(t *testing.T) {
	cfg := Config{
		ChunkSizeChars: 5,
		OverlapChars:   0,
		MaxChunks:      3,
	}
	// "aaaaabbbbbcccccdddddeeeee" = 25 chars -> would be 5 chunks, capped at 3
	text := "aaaaabbbbbcccccdddddeeeee"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 3)
	// Verify only 3 chunks were created
	for i, c := range chunks {
		require.Equal(t, i, c.Index)
	}
}

func TestChunkContentHash(t *testing.T) {
	cfg := DefaultConfig()
	text := "The quick brown fox jumps over the lazy dog."
	chunks1 := ChunkText(text, cfg)
	chunks2 := ChunkText(text, cfg)
	require.Len(t, chunks1, 1)
	require.Len(t, chunks2, 1)
	require.Equal(t, chunks1[0].ContentHash, chunks2[0].ContentHash)
}

func TestChunkDifferentContentHash(t *testing.T) {
	cfg := DefaultConfig()
	chunksA := ChunkText("Hello, world!", cfg)
	chunksB := ChunkText("Goodbye, world!", cfg)
	require.Len(t, chunksA, 1)
	require.Len(t, chunksB, 1)
	require.NotEqual(t, chunksA[0].ContentHash, chunksB[0].ContentHash)
}

func TestChunkIndexing(t *testing.T) {
	cfg := Config{
		ChunkSizeChars: 5,
		OverlapChars:   0,
		MaxChunks:      500,
	}
	text := "aaaaabbbbbcccccdddddeeeee"
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 5)
	for i, c := range chunks {
		require.Equal(t, i, c.Index, "chunk %d should have Index=%d", i, i)
	}
}

func TestChunkNegativeStepFallback(t *testing.T) {
	// When OverlapChars >= ChunkSizeChars, step should fall back to 1
	cfg := Config{
		ChunkSizeChars: 10,
		OverlapChars:   20,
		MaxChunks:      500,
	}
	text := "01234567890123456789" // 20 chars
	chunks := ChunkText(text, cfg)
	// step = 10 - 20 = -10 -> clamped to 1, so many tiny overlapping chunks
	require.NotEmpty(t, chunks)
	// First chunk should be "0123456789"
	require.Equal(t, "0123456789", chunks[0].Content)
	// Should produce more than 2 chunks
	require.Greater(t, len(chunks), 2)
}

func TestChunkWithWhitespacePreservation(t *testing.T) {
	// Internal whitespace is preserved, leading/trailing is trimmed
	cfg := DefaultConfig()
	text := "  hello   world  "
	chunks := ChunkText(text, cfg)
	require.Len(t, chunks, 1)
	require.Equal(t, "hello   world", chunks[0].Content)
	require.Equal(t, 13, chunks[0].CharCount)
}
