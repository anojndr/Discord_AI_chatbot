package processors

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"DiscordAIChatbot/internal/storage"
)

// ChartProcessor handles detection and execution of chart code
type ChartProcessor struct {
	tempDir        string
	venvDir        string
	libraryManager *storage.ChartLibraryManager
	initialized    bool
}

// ChartImage represents a generated chart image
type ChartImage struct {
	Data     []byte
	Filename string
}

// findProjectRoot finds the project root directory by looking for go.mod
func findProjectRoot() (string, error) {
	// Start from current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree looking for go.mod
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root directory
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("go.mod not found in directory tree")
}

// NewChartProcessor creates a new chart processor instance
func NewChartProcessor(dbURL string) *ChartProcessor {
	// Create temp directory for chart generation (for temporary files)
	tempDir := filepath.Join(os.TempDir(), "discord_ai_charts")
	_ = os.MkdirAll(tempDir, 0755)

	// Create virtual environment in a permanent location
	// Try to find the project root by looking for go.mod
	projectRoot, err := findProjectRoot()
	if err != nil {
		log.Printf("Failed to find project root, using working directory: %v", err)
		projectRoot, _ = os.Getwd()
		if projectRoot == "" {
			projectRoot = os.TempDir()
		}
	}

	// Place virtual environment in project directory for persistence
	venvDir := filepath.Join(projectRoot, "chart_venv")

	// Initialize chart library manager
	var libraryManager *storage.ChartLibraryManager
	if dbURL != "" {
		libraryManager = storage.NewChartLibraryManager(dbURL)
	}

	cp := &ChartProcessor{
		tempDir:        tempDir,
		venvDir:        venvDir,
		libraryManager: libraryManager,
		initialized:    false,
	}

	return cp
}

// getPythonCommands returns a list of Python commands to try in order of preference
func (cp *ChartProcessor) getPythonCommands() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"python", "py", "python3"}
	case "darwin": // macOS
		return []string{"python3", "python"}
	default: // Linux and other Unix-like systems
		return []string{"python3", "python"}
	}
}

// getPythonExecutablePath returns the path to the Python executable in the virtual environment
func (cp *ChartProcessor) getPythonExecutablePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(cp.venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(cp.venvDir, "bin", "python")
}

// getPipExecutablePath returns the path to the pip executable in the virtual environment
func (cp *ChartProcessor) getPipExecutablePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(cp.venvDir, "Scripts", "pip.exe")
	}
	return filepath.Join(cp.venvDir, "bin", "pip")
}

// initializeVenv creates and sets up a Python virtual environment
func (cp *ChartProcessor) initializeVenv() error {
	// Check if virtual environment already exists
	if _, err := os.Stat(cp.venvDir); err == nil {
		// Virtual environment already exists, check if it's valid
		pythonPath := cp.getPythonExecutablePath()
		if _, err := os.Stat(pythonPath); err == nil {
			log.Printf("Virtual environment already exists at: %s", cp.venvDir)
			return nil
		}
	}

	// Create virtual environment
	log.Printf("Creating Python virtual environment at: %s", cp.venvDir)

	// Try different Python commands based on OS and availability
	pythonCmds := cp.getPythonCommands()
	var lastErr error
	var stderr bytes.Buffer

	for _, pythonCmd := range pythonCmds {
		log.Printf("Trying to create venv with: %s", pythonCmd)
		cmd := exec.Command(pythonCmd, "-m", "venv", cp.venvDir)
		cmd.Dir = cp.tempDir
		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("failed with %s: %w, stderr: %s", pythonCmd, err, stderr.String())
			log.Printf("Command %s failed: %v", pythonCmd, err)
			continue
		}

		log.Printf("Successfully created virtual environment with %s", pythonCmd)
		return nil
	}

	return fmt.Errorf("failed to create virtual environment with any Python command: %w", lastErr)
}

// ProcessResponse detects and executes chart code in the response
func (cp *ChartProcessor) ProcessResponse(ctx context.Context, response string) ([]ChartImage, error) {
	// Lazy initialization - only initialize when chart processing is actually needed
	if !cp.initialized {
		if err := cp.initializeVenv(); err != nil {
			return nil, fmt.Errorf("failed to initialize chart processor: %w", err)
		}
		cp.initialized = true
	}

	// Detect Python chart code blocks
	codeBlocks := cp.detectChartCode(response)
	if len(codeBlocks) == 0 {
		return nil, nil
	}

	var chartImages []ChartImage

	for i, code := range codeBlocks {
		// Update library usage statistics
		cp.updateLibraryUsage(code)

		// Generate unique filename for this chart
		timestamp := time.Now().Unix()
		filename := fmt.Sprintf("chart_%d_%d.png", timestamp, i)

		// Execute the chart code and generate image
		imageData, err := cp.executeChartCode(ctx, code, filename)
		if err != nil {
			log.Printf("Failed to execute chart code %d: %v", i, err)
			continue
		}

		chartImages = append(chartImages, ChartImage{
			Data:     imageData,
			Filename: filename,
		})
	}

	return chartImages, nil
}

// detectChartCode detects Python code blocks that generate charts
func (cp *ChartProcessor) detectChartCode(response string) []string {
	// Regex to match Python code blocks
	codeBlockRegex := regexp.MustCompile("(?s)```python\n(.*?)\n```")
	matches := codeBlockRegex.FindAllStringSubmatch(response, -1)

	var chartCode []string

	for _, match := range matches {
		if len(match) >= 2 {
			code := match[1]

			// Check if this code block contains chart/plotting libraries
			if cp.isChartCode(code) {
				chartCode = append(chartCode, code)
			}
		}
	}

	return chartCode
}

// isChartCode checks if the code uses chart/plotting libraries
func (cp *ChartProcessor) isChartCode(code string) bool {
	// Common chart libraries and methods
	chartIndicators := []string{
		"matplotlib",
		"pyplot",
		"plt.",
		"seaborn",
		"sns.",
		"plotly",
		"bokeh",
		"altair",
		"pygal",
		"chart",
		".plot(",
		".bar(",
		".scatter(",
		".hist(",
		".line(",
		".pie(",
		".show()",
		"plt.show()",
		"plt.savefig(",
		"fig.savefig(",
	}

	codeLower := strings.ToLower(code)

	for _, indicator := range chartIndicators {
		if strings.Contains(codeLower, strings.ToLower(indicator)) {
			return true
		}
	}

	return false
}

// executeChartCode executes Python chart code and returns the generated image
func (cp *ChartProcessor) executeChartCode(ctx context.Context, code string, filename string) ([]byte, error) {
	// Modify the code to save the chart instead of showing it
	modifiedCode := cp.modifyCodeForSaving(code, filename)

	// Create temporary Python file
	tempFile := filepath.Join(cp.tempDir, fmt.Sprintf("chart_%d.py", time.Now().UnixNano()))
	defer func() { _ = os.Remove(tempFile) }()

	// Write the modified code to temporary file
	if err := os.WriteFile(tempFile, []byte(modifiedCode), 0644); err != nil {
		return nil, fmt.Errorf("failed to write temporary Python file: %w", err)
	}

	// Execute the Python code using virtual environment
	pythonPath := cp.getPythonExecutablePath()
	cmd := exec.CommandContext(ctx, pythonPath, tempFile)
	cmd.Dir = cp.tempDir

	// Set environment variables for cross-platform compatibility
	env := os.Environ()
	if runtime.GOOS == "windows" {
		// On Windows, ensure proper encoding for matplotlib
		env = append(env, "PYTHONIOENCODING=utf-8")
	}
	// Set matplotlib backend for all platforms
	env = append(env, "MPLBACKEND=Agg")
	cmd.Env = env

	// Capture output for debugging
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Try to install missing libraries and retry
		if strings.Contains(stderr.String(), "ModuleNotFoundError") || strings.Contains(stderr.String(), "ImportError") {
			log.Printf("Installing missing Python libraries...")
			if installErr := cp.installMissingLibraries(ctx, stderr.String()); installErr != nil {
				log.Printf("Failed to install libraries: %v", installErr)
			} else {
				// Retry execution after installing libraries
				pythonPath := cp.getPythonExecutablePath()
				cmd = exec.CommandContext(ctx, pythonPath, tempFile)
				cmd.Dir = cp.tempDir
				cmd.Env = env
				stderr.Reset()
				stdout.Reset()
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				if retryErr := cmd.Run(); retryErr != nil {
					return nil, fmt.Errorf("failed to execute Python code after installing libraries: %w, stderr: %s", retryErr, stderr.String())
				}
			}
		} else {
			return nil, fmt.Errorf("failed to execute Python code: %w, stderr: %s", err, stderr.String())
		}
	}

	// Read the generated image file
	imagePath := filepath.Join(cp.tempDir, filename)
	defer func() { _ = os.Remove(imagePath) }()

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read generated image: %w", err)
	}

	return imageData, nil
}

// modifyCodeForSaving modifies the Python code to save the chart instead of showing it
func (cp *ChartProcessor) modifyCodeForSaving(code string, filename string) string {
	lines := strings.Split(code, "\n")
	var modifiedLines []string

	// Add necessary imports at the beginning with cross-platform compatibility
	modifiedLines = append(modifiedLines, "import os")
	modifiedLines = append(modifiedLines, "import matplotlib")
	modifiedLines = append(modifiedLines, "matplotlib.use('Agg')  # Use non-interactive backend")
	modifiedLines = append(modifiedLines, "import matplotlib.pyplot as plt")
	modifiedLines = append(modifiedLines, "")

	// Process each line of the original code
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip import statements for matplotlib (we already added them)
		if strings.HasPrefix(trimmed, "import matplotlib") ||
			strings.HasPrefix(trimmed, "from matplotlib import") ||
			strings.HasPrefix(trimmed, "matplotlib.use(") {
			continue
		}

		// Replace plt.show() with plt.savefig()
		if strings.Contains(trimmed, "plt.show()") {
			modifiedLines = append(modifiedLines, strings.ReplaceAll(line, "plt.show()", fmt.Sprintf("plt.savefig(r'%s', dpi=300, bbox_inches='tight')", filename)))
		} else if strings.Contains(trimmed, ".show()") && !strings.Contains(trimmed, "plt.savefig(") {
			// Replace other .show() calls with savefig
			modifiedLines = append(modifiedLines, line)
			modifiedLines = append(modifiedLines, fmt.Sprintf("plt.savefig(r'%s', dpi=300, bbox_inches='tight')", filename))
		} else {
			modifiedLines = append(modifiedLines, line)
		}
	}

	// If no explicit show() or savefig() was found, add savefig at the end
	codeStr := strings.Join(modifiedLines, "\n")
	if !strings.Contains(codeStr, "savefig(") && !strings.Contains(codeStr, "show()") {
		modifiedLines = append(modifiedLines, fmt.Sprintf("plt.savefig(r'%s', dpi=300, bbox_inches='tight')", filename))
	}

	// Add plt.close() to free memory
	modifiedLines = append(modifiedLines, "plt.close()")

	return strings.Join(modifiedLines, "\n")
}

// installMissingLibraries attempts to install missing Python libraries
func (cp *ChartProcessor) installMissingLibraries(ctx context.Context, errorMsg string) error {
	// Ensure virtual environment exists before installing any packages
	if err := cp.initializeVenv(); err != nil {
		return fmt.Errorf("failed to initialize virtual environment: %w", err)
	}

	// Common libraries that might be missing
	libraries := []string{
		"matplotlib",
		"seaborn",
		"plotly",
		"bokeh",
		"altair",
		"pygal",
		"pandas",
		"numpy",
	}

	// Extract module names from error message
	moduleNotFoundRegex := regexp.MustCompile(`No module named '([^']+)'`)
	importErrorRegex := regexp.MustCompile(`ImportError: cannot import name '([^']+)'`)

	matches := moduleNotFoundRegex.FindAllStringSubmatch(errorMsg, -1)
	matches = append(matches, importErrorRegex.FindAllStringSubmatch(errorMsg, -1)...)

	var missingLibs []string
	for _, match := range matches {
		if len(match) >= 2 {
			missingLibs = append(missingLibs, match[1])
		}
	}

	// If no specific libraries found in error, try installing common ones
	if len(missingLibs) == 0 {
		missingLibs = libraries
	}

	// Install missing libraries using virtual environment
	pipPath := cp.getPipExecutablePath()
	for _, lib := range missingLibs {
		// Check if library is already installed in database
		if cp.libraryManager != nil {
			isInstalled, err := cp.libraryManager.IsLibraryInstalled(lib)
			if err != nil {
				log.Printf("Failed to check if %s is installed: %v", lib, err)
			} else if isInstalled {
				log.Printf("Library %s is already installed according to database", lib)
				// Update last used timestamp
				_ = cp.libraryManager.UpdateLastUsed(lib)
				continue
			}
		}

		log.Printf("Installing Python library in venv: %s", lib)
		cmd := exec.CommandContext(ctx, pipPath, "install", lib)

		// Set environment variables for cross-platform pip usage
		env := os.Environ()
		if runtime.GOOS == "windows" {
			// On Windows, ensure proper encoding for pip
			env = append(env, "PYTHONIOENCODING=utf-8")
		}
		cmd.Env = env

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			log.Printf("Failed to install %s in venv: %v, stderr: %s", lib, err, stderr.String())
			// Mark as failed installation in database
			if cp.libraryManager != nil {
				_ = cp.libraryManager.MarkLibraryUninstalled(lib)
			}
			continue
		}

		log.Printf("Successfully installed %s in venv", lib)

		// Mark as successfully installed in database
		if cp.libraryManager != nil {
			if err := cp.libraryManager.MarkLibraryInstalled(lib, "latest"); err != nil {
				log.Printf("Failed to mark %s as installed in database: %v", lib, err)
			}
		}
	}

	return nil
}

// updateLibraryUsage updates library usage statistics in the database
func (cp *ChartProcessor) updateLibraryUsage(code string) {
	if cp.libraryManager == nil {
		return
	}

	// Common chart libraries and their import patterns
	libraryPatterns := map[string][]string{
		"matplotlib": {"import matplotlib", "from matplotlib", "matplotlib.pyplot", "plt."},
		"seaborn":    {"import seaborn", "from seaborn", "sns."},
		"plotly":     {"import plotly", "from plotly", "plotly."},
		"bokeh":      {"import bokeh", "from bokeh", "bokeh."},
		"altair":     {"import altair", "from altair", "alt."},
		"pygal":      {"import pygal", "from pygal", "pygal."},
		"pandas":     {"import pandas", "from pandas", "pd."},
		"numpy":      {"import numpy", "from numpy", "np."},
	}

	codeLower := strings.ToLower(code)

	for library, patterns := range libraryPatterns {
		for _, pattern := range patterns {
			if strings.Contains(codeLower, strings.ToLower(pattern)) {
				if err := cp.libraryManager.UpdateLastUsed(library); err != nil {
					log.Printf("Failed to update last used for %s: %v", library, err)
				}
				break
			}
		}
	}
}

// GetLibraryStats returns statistics about chart libraries
func (cp *ChartProcessor) GetLibraryStats() (map[string]interface{}, error) {
	if cp.libraryManager == nil {
		return nil, fmt.Errorf("library manager not initialized")
	}
	return cp.libraryManager.GetLibraryStats()
}

// GetInstalledLibraries returns a list of installed chart libraries
func (cp *ChartProcessor) GetInstalledLibraries() ([]storage.ChartLibrary, error) {
	if cp.libraryManager == nil {
		return nil, fmt.Errorf("library manager not initialized")
	}
	return cp.libraryManager.GetInstalledLibraries()
}

// PreinstallCommonLibraries preinstalls common chart libraries
func (cp *ChartProcessor) PreinstallCommonLibraries(ctx context.Context) error {
	// Ensure virtual environment exists before installing any packages
	if err := cp.initializeVenv(); err != nil {
		return fmt.Errorf("failed to initialize virtual environment: %w", err)
	}

	commonLibs := []string{
		"matplotlib",
		"seaborn",
		"plotly",
		"pandas",
		"numpy",
	}

	pipPath := cp.getPipExecutablePath()
	for _, lib := range commonLibs {
		// Check if already installed in database
		if cp.libraryManager != nil {
			isInstalled, err := cp.libraryManager.IsLibraryInstalled(lib)
			if err != nil {
				log.Printf("Failed to check if %s is installed: %v", lib, err)
			} else if isInstalled {
				log.Printf("Library %s is already installed", lib)
				continue
			}
		}

		log.Printf("Preinstalling Python library: %s", lib)
		cmd := exec.CommandContext(ctx, pipPath, "install", lib)

		// Set environment variables for cross-platform pip usage
		env := os.Environ()
		if runtime.GOOS == "windows" {
			// On Windows, ensure proper encoding for pip
			env = append(env, "PYTHONIOENCODING=utf-8")
		}
		cmd.Env = env

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			log.Printf("Failed to preinstall %s: %v, stderr: %s", lib, err, stderr.String())
			continue
		}

		log.Printf("Successfully preinstalled %s", lib)

		// Mark as installed in database
		if cp.libraryManager != nil {
			if err := cp.libraryManager.MarkLibraryInstalled(lib, "latest"); err != nil {
				log.Printf("Failed to mark %s as installed in database: %v", lib, err)
			}
		}
	}

	return nil
}

// Cleanup removes temporary files but preserves the virtual environment
func (cp *ChartProcessor) Cleanup() error {
	// Close database connection if it exists
	if cp.libraryManager != nil {
		if err := cp.libraryManager.Close(); err != nil {
			log.Printf("Failed to close library manager: %v", err)
		}
	}

	// Only remove temporary files, keep virtual environment for persistence
	return os.RemoveAll(cp.tempDir)
}
