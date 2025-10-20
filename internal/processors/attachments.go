package processors

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
)

// ProcessAttachments processes Discord message attachments and returns images, audio, and text content
func ProcessAttachments(ctx context.Context, attachments []*discordgo.MessageAttachment, fileProcessor *FileProcessor) ([]messaging.ImageContent, []messaging.AudioContent, []messaging.PDFContent, string, bool, bool, error) {
	log.Println("Starting attachment processing...")
	// Launch one goroutine per attachment without an artificial semaphore limit.

	// We need to preserve the order of attachments so that they appear in the
	// same order when forwarded to the LLM. Because we are processing attachments
	// concurrently, we first keep the result together with its original index
	// and then sort at the end.
	type indexedResult struct {
		idx               int
		img               messaging.ImageContent
		audio             messaging.AudioContent
		pdf               messaging.PDFContent
		text              string
		isBad             bool
		shouldProcessURLs bool
		err               error
	}

	// Early exit if no attachments
	if len(attachments) == 0 {
		log.Println("No attachments to process.")
		return nil, nil, nil, "", false, false, nil
	}

	resultsChan := make(chan indexedResult, len(attachments))
	var wg sync.WaitGroup

	for idx, att := range attachments {
		wg.Add(1)

		go func(index int, attachment *discordgo.MessageAttachment) {
			defer wg.Done()
			log.Printf("Processing attachment %d: %s", index, attachment.Filename)

			// Create a context with a timeout for the download
			dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			// Check supported types (content-type first, then extension fallback)
			isImage := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "image/")
			isAudio := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "audio/")
			isText := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "text/")
			isPDFByCT := attachment.ContentType != "" && strings.HasPrefix(attachment.ContentType, "application/pdf")
			isPDFByExt := strings.EqualFold(filepath.Ext(attachment.Filename), ".pdf")
			isPDF := isPDFByCT || isPDFByExt
			isTextByExt := fileProcessor.isTextFileByExtension(attachment.Filename)

			if !isImage && !isAudio && !isText && !isPDF && !isTextByExt {
				log.Printf("Attachment %d (%s) is an unsupported type.", index, attachment.Filename)
				resultsChan <- indexedResult{idx: index, isBad: true}
				return
			}

			// Download attachment
			req, err := http.NewRequestWithContext(dlCtx, "GET", attachment.URL, nil)
			if err != nil {
				log.Printf("Error creating request for attachment %d: %v", index, err)
				resultsChan <- indexedResult{idx: index, err: fmt.Errorf("failed to create download request: %w", err)}
				return
			}

			log.Printf("Downloading attachment %d: %s", index, attachment.URL)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("Error downloading attachment %d: %v", index, err)
				resultsChan <- indexedResult{idx: index, err: fmt.Errorf("failed to download attachment: %w", err)}
				return
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					log.Printf("Failed to close response body for attachment %d: %v", index, err)
				}
			}()
			log.Printf("Finished downloading attachment %d.", index)

			log.Printf("Reading data for attachment %d...", index)
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("Error reading data for attachment %d: %v", index, err)
				resultsChan <- indexedResult{idx: index, err: fmt.Errorf("failed to read attachment: %w", err)}
				return
			}
			log.Printf("Finished reading data for attachment %d. Size: %d bytes", index, len(data))

			if isImage {
				log.Printf("Processing attachment %d as image...", index)
				// Image attachment -> encode as data URL
				encodedData := base64.StdEncoding.EncodeToString(data)
				dataURL := fmt.Sprintf("data:%s;base64,%s", attachment.ContentType, encodedData)
				resultsChan <- indexedResult{idx: index, img: messaging.ImageContent{
					Type:     "image_url",
					ImageURL: messaging.ImageURL{URL: dataURL},
				}}
				log.Printf("Finished processing image attachment %d.", index)
				return
			} else if isAudio {
				log.Printf("Processing attachment %d as audio...", index)
				// Audio attachment -> store raw data
				resultsChan <- indexedResult{idx: index, audio: messaging.AudioContent{
					Type:     "audio_file",
					MIMEType: attachment.ContentType,
					URL:      attachment.URL,
					Data:     data,
				}}
				log.Printf("Finished processing audio attachment %d.", index)
				return
			} else if isPDF {
				log.Printf("Processing attachment %d as PDF...", index)
				// PDF attachment -> store raw data AND extract text for non-Gemini models
				extractedText, shouldProcessURLs, err := fileProcessor.ProcessFile(data, func() string {
					if attachment.ContentType != "" {
						return attachment.ContentType
					}
					// Fallback content-type when missing but extension indicates PDF
					return "application/pdf"
				}(), attachment.Filename)

				var fileTypeInfo string = fmt.Sprintf("**📄 PDF Document: %s**\n", attachment.Filename)
				result := indexedResult{
					idx: index,
					pdf: messaging.PDFContent{
						Type: "pdf_file",
						MIMEType: func() string {
							if attachment.ContentType != "" {
								return attachment.ContentType
							}
							return "application/pdf"
						}(),
						URL:      attachment.URL,
						Filename: attachment.Filename,
						Data:     data,
					},
				}

				if err != nil {
					// If text extraction fails, create a user-facing error message.
					// This is better than failing silently.
					log.Printf("Failed to extract text from PDF %d: %v", index, err)
					result.text = fmt.Sprintf("%s\n> ⚠️ **Error:** Could not extract text from this PDF.", fileTypeInfo)
				} else if strings.TrimSpace(extractedText) != "" {
					log.Printf("Extracted %d chars from PDF %d.", len(extractedText), index)
					result.text = fileTypeInfo + extractedText
					result.shouldProcessURLs = shouldProcessURLs
				} else {
					log.Printf("PDF %d is empty or has no text.", index)
					// If the PDF is empty, provide a message indicating that.
					result.text = fmt.Sprintf("%s\n> 📄 This PDF appears to be empty or contains no text.", fileTypeInfo)
				}

				resultsChan <- result
				log.Printf("Finished processing PDF attachment %d.", index)
				return
			}

			log.Printf("Processing attachment %d as text...", index)
			// Text attachment -> process via FileProcessor
			extractedText, shouldProcessURLs, err := fileProcessor.ProcessFile(data, attachment.ContentType, attachment.Filename)
			if err != nil {
				log.Printf("Error processing text attachment %d: %v", index, err)
				resultsChan <- indexedResult{idx: index, isBad: true}
				return
			}

			var fileTypeInfo string
			switch {
			case isText:
				fileTypeInfo = fmt.Sprintf("**📝 Text File: %s**\n", attachment.Filename)
			case isTextByExt:
				fileTypeInfo = fmt.Sprintf("**📄 File: %s**\n", attachment.Filename)
			}
			resultsChan <- indexedResult{idx: index, text: fileTypeInfo + extractedText, shouldProcessURLs: shouldProcessURLs}
			log.Printf("Finished processing text attachment %d.", index)
		}(idx, att)
	}

	// Wait for all goroutines to finish
	log.Println("Waiting for all attachment processing goroutines to finish...")
	wg.Wait()
	log.Println("All goroutines finished.")
	close(resultsChan)

	// Collect and sort results from the channel
	log.Println("Collecting results from channel...")
	allResults := make([]indexedResult, 0, len(attachments))
	for res := range resultsChan {
		log.Printf("Collected result for index %d.", res.idx)
		allResults = append(allResults, res)
	}
	sort.Slice(allResults, func(i, j int) bool { return allResults[i].idx < allResults[j].idx })
	log.Println("Finished collecting and sorting results.")

	var (
		orderedImages     []messaging.ImageContent
		orderedAudio      []messaging.AudioContent
		orderedPDFs       []messaging.PDFContent
		textParts         []string
		hasBadAttachments bool
		shouldProcessURLs bool
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
		if res.pdf.Type != "" {
			orderedPDFs = append(orderedPDFs, res.pdf)
		}
		if res.text != "" {
			textParts = append(textParts, res.text)
		}
		if res.shouldProcessURLs {
			shouldProcessURLs = true
		}
	}

	if firstErr != nil {
		log.Printf("Attachment processing finished with an error: %v", firstErr)
		return nil, nil, nil, "", hasBadAttachments, false, firstErr
	}

	log.Println("Attachment processing finished successfully.")
	return orderedImages, orderedAudio, orderedPDFs, strings.Join(textParts, "\n\n"), hasBadAttachments, shouldProcessURLs, nil
}
