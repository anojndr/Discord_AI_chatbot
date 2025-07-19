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

// ProcessAttachments processes Discord message attachments and returns images, audio, and text content
func ProcessAttachments(ctx context.Context, attachments []*discordgo.MessageAttachment, fileProcessor *FileProcessor) ([]messaging.ImageContent, []messaging.AudioContent, string, bool, error) {
	// Launch one goroutine per attachment without an artificial semaphore limit.

	// We need to preserve the order of attachments so that they appear in the
	// same order when forwarded to the LLM. Because we are processing attachments
	// concurrently, we first keep the result together with its original index
	// and then sort at the end.
	type indexedResult struct {
		idx   int
		img   messaging.ImageContent
		audio messaging.AudioContent
		text  string
		isBad bool
		err   error
	}

	// Early exit if no attachments
	if len(attachments) == 0 {
		return nil, nil, "", false, nil
	}

	resultsChan := make(chan indexedResult, len(attachments))
	var wg sync.WaitGroup

	for idx, att := range attachments {
		wg.Add(1)

		go func(index int, attachment *discordgo.MessageAttachment) {
			defer wg.Done()

			// Check supported types (extension detection first)
			isImage := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "image/")
			isAudio := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "audio/")
			isText := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "text/")
			isPDF := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "application/pdf")
			isTextByExt := fileProcessor.isTextFileByExtension(attachment.Filename)

			if !isImage && !isAudio && !isText && !isPDF && !isTextByExt {
				resultsChan <- indexedResult{idx: index, isBad: true}
				return
			}

			// Download attachment
			resp, err := http.Get(attachment.URL)
			if err != nil {
				resultsChan <- indexedResult{idx: index, err: fmt.Errorf("failed to download attachment: %w", err)}
				return
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					log.Printf("Failed to close response body: %v", err)
				}
			}()

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				resultsChan <- indexedResult{idx: index, err: fmt.Errorf("failed to read attachment: %w", err)}
				return
			}

			if isImage {
				// Image attachment -> encode as data URL
				encodedData := base64.StdEncoding.EncodeToString(data)
				dataURL := fmt.Sprintf("data:%s;base64,%s", attachment.ContentType, encodedData)
				resultsChan <- indexedResult{idx: index, img: messaging.ImageContent{
					Type:     "image_url",
					ImageURL: messaging.ImageURL{URL: dataURL},
				}}
				return
			} else if isAudio {
				// Audio attachment -> store raw data
				resultsChan <- indexedResult{idx: index, audio: messaging.AudioContent{
					Type:     "audio_file",
					MIMEType: attachment.ContentType,
					URL:      attachment.URL,
					Data:     data,
				}}
				return
			}

			// Text or PDF attachment -> process via FileProcessor
			extractedText, err := fileProcessor.ProcessFile(data, attachment.ContentType, attachment.Filename)
			if err != nil {
				resultsChan <- indexedResult{idx: index, isBad: true}
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
			resultsChan <- indexedResult{idx: index, text: fileTypeInfo + extractedText}
		}(idx, att)
	}

	// Wait for all goroutines to finish
	wg.Wait()
	close(resultsChan)

	// Collect and sort results from the channel
	allResults := make([]indexedResult, 0, len(attachments))
	for res := range resultsChan {
		allResults = append(allResults, res)
	}
	sort.Slice(allResults, func(i, j int) bool { return allResults[i].idx < allResults[j].idx })

	var (
		orderedImages     []messaging.ImageContent
		orderedAudio      []messaging.AudioContent
		textParts         []string
		hasBadAttachments bool
		firstErr          error
	)

	for _, res := range allResults {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		if res.isBad {
			hasBadAttachments = true
		}
		if res.img.Type != "" {
			orderedImages = append(orderedImages, res.img)
		}
		if res.audio.Type != "" {
			orderedAudio = append(orderedAudio, res.audio)
		}
		if res.text != "" {
			textParts = append(textParts, res.text)
		}
	}

	if firstErr != nil {
		return nil, nil, "", hasBadAttachments, firstErr
	}

	return orderedImages, orderedAudio, strings.Join(textParts, "\n\n"), hasBadAttachments, nil
}
