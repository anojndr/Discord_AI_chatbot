package processors

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
)

// ProcessAttachments processes Discord message attachments and returns images and text content
func ProcessAttachments(ctx context.Context, attachments []*discordgo.MessageAttachment, fileProcessor *FileProcessor) ([]messaging.ImageContent, string, bool, error) {
	// Launch one goroutine per attachment without an artificial semaphore limit.

	// We need to preserve the order of image attachments so that the images the
	// user supplies appear in the same order when forwarded to the LLM. Because
	// we are processing attachments concurrently, we first keep the image result
	// together with its original index and then sort at the end.
	type indexedImage struct {
		idx int
		img messaging.ImageContent
	}

	var (
		imageResults      []indexedImage
		textParts         []string
		hasBadAttachments bool
		mu                sync.Mutex
		wg                sync.WaitGroup
		firstErr          error
	)

	// Early exit if no attachments
	if len(attachments) == 0 {
		return nil, "", false, nil
	}

	for idx, att := range attachments {
		wg.Add(1)

		// Capture loop variables
		attachment := att
		index := idx

		go func() {
			defer wg.Done()

			// Check supported types (extension detection first)
			isImage := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "image/")
			isText := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "text/")
			isPDF := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "application/pdf")
			isTextByExt := fileProcessor.isTextFileByExtension(attachment.Filename)

			if !isImage && !isText && !isPDF && !isTextByExt {
				mu.Lock()
				hasBadAttachments = true
				mu.Unlock()
				return
			}

			// Download attachment
			resp, err := http.Get(attachment.URL)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to download attachment: %w", err)
				}
				mu.Unlock()
				return
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					log.Printf("Failed to close response body: %v", err)
				}
			}()

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to read attachment: %w", err)
				}
				mu.Unlock()
				return
			}

			if isImage {
				// Image attachment -> encode as data URL
				encodedData := base64.StdEncoding.EncodeToString(data)
				dataURL := fmt.Sprintf("data:%s;base64,%s", attachment.ContentType, encodedData)

				mu.Lock()
				imageResults = append(imageResults, indexedImage{idx: index, img: messaging.ImageContent{
					Type:     "image_url",
					ImageURL: messaging.ImageURL{URL: dataURL},
				}})
				mu.Unlock()
				return
			}

			// Text or PDF attachment -> process via FileProcessor
			extractedText, err := fileProcessor.ProcessFile(data, attachment.ContentType, attachment.Filename)
			if err != nil {
				mu.Lock()
				hasBadAttachments = true
				mu.Unlock()
				return
			}

			var fileTypeInfo string
			switch {
			case isPDF:
				fileTypeInfo = fmt.Sprintf("**📄 PDF File: %s**\n", attachment.Filename)
			case isText:
				fileTypeInfo = fmt.Sprintf("**📝 Text File: %s**\n", attachment.Filename)
			case isTextByExt:
				fileTypeInfo = fmt.Sprintf("**📄 File: %s**\n", attachment.Filename)
			}

			mu.Lock()
			textParts = append(textParts, fileTypeInfo+extractedText)
			mu.Unlock()
		}()
	}

	// Wait for all goroutines to finish
	wg.Wait()

	if firstErr != nil {
		return nil, "", hasBadAttachments, firstErr
	}

	// Re-establish the original order for images
	sort.Slice(imageResults, func(i, j int) bool { return imageResults[i].idx < imageResults[j].idx })
	var orderedImages []messaging.ImageContent
	for _, res := range imageResults {
		orderedImages = append(orderedImages, res.img)
	}

	return orderedImages, strings.Join(textParts, "\n\n"), hasBadAttachments, nil
}
