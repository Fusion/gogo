package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"dario.cat/mergo"
	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

type Auth struct {
	Token string `toml:"token"`
}

type Paths struct {
	TargetDir string `toml:"targetdir"`
}

type Repository struct {
	Name    string `toml:"name"`
	File    string `toml:"file"`
	Comment string `toml:"comment"`
}

type Config struct {
	Auth         Auth         `toml:"auth"`
	Paths        Paths        `toml:"paths"`
	Repositories []Repository `toml:"repositories"`
}

type ReleaseAsset struct {
	BrowserDownloadURL string `json:"browser_download_url"`
	Name               string `json:"name"`
}

type ERepoStatus int

const (
	RepoOK ERepoStatus = iota
	RepoKO
	RepoExist
)

type EAssetFormat int

const (
	BinaryFormat EAssetFormat = iota
	TarballFormat
	TargzipFormat
	ZipFormat
)

type RepoStatus struct {
	Repo   *Repository
	Status ERepoStatus
	Format EAssetFormat
	Asset  string
	Url    string
}

var (
	VERSION = "0.0.2"

	ArchEquiv = map[string][]string{
		"amd64": {"amd64", "x86_64"},
		"arm64": {"arm64", "amd64", "x86_64"},
	}
	OSEquiv = map[string][]string{
		"darwin": {"darwin", "macos"},
	}
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("gogo v%s (https://github.com/fusion/gogo)\n\n", VERSION)
		fmt.Printf("Usage: %s <action> [-config <config-file>] [-update]\n\nAvailable actions:\n", os.Args[0])
		fmt.Println("  list            list available commands")
		fmt.Println("  fetch [command] fetch one or all commands")
		fmt.Println("\nFlags:")
		fmt.Println("  -config <config-file> path to a configuration file or directory")
		fmt.Println("  -update               update commands if already installed")
		os.Exit(1)
	}
	command := os.Args[1]
	args := os.Args[2:]

	listCmd := flag.NewFlagSet("list", flag.ExitOnError)
	listConfigPath := listCmd.String("config", "config.toml", "Path to the TOML configuration file")
	fetchCmd := flag.NewFlagSet("fetch", flag.ExitOnError)
	fetchConfigPath := fetchCmd.String("config", "config.toml", "Path to the TOML configuration file")
	fetchUpdate := fetchCmd.Bool("update", false, "Update commands if already installed")

	switch command {
	case "list":
		listCmd.Parse(args)
		list(*listConfigPath)
	case "fetch":
		if strings.HasPrefix(args[0], "-") {
			fetchCmd.Parse(args)
			fetch(*fetchConfigPath, *fetchUpdate, nil)
		} else {
			fetchCmd.Parse(args[1:])
			fetch(*fetchConfigPath, *fetchUpdate, &args[0])
		}
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func list(configPath string) {
	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		os.Exit(1)
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		StyleFunc(
			func(_, col int) lipgloss.Style {
				switch col {
				case 1:
					return lipgloss.NewStyle().Width(48).Padding(0, 1).Align(lipgloss.Left)
				default:
					return lipgloss.NewStyle().Padding(0, 1)
				}
			},
		).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("99")))
	t.Headers("Binary", "Description")

	for _, repo := range config.Repositories {
		t.Row(repo.File, repo.Comment)
	}
	fmt.Println(t)
}

func fetch(configPath string, update bool, command *string) {
	hostArch := strings.ToLower(runtime.GOARCH)
	hostOS := strings.ToLower(runtime.GOOS)

	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		os.Exit(1)
	}

	if config.Paths.TargetDir == "" {
		fmt.Printf("Target directory not set, using current directory\n")
		config.Paths.TargetDir = "."
	}
	config.Paths.TargetDir, err = expandPath(config.Paths.TargetDir)
	if err != nil {
		fmt.Printf("Error expanding target directory: %v\n", err)
		os.Exit(1)
	}
	if err := checkTargetDir(config.Paths.TargetDir); err != nil {
		fmt.Printf("Error checking target directory: %v\n", err)
		os.Exit(1)
	}

	repoStatusList := []RepoStatus{}

	fmt.Printf("[Preflight]\n")
	for _, repo := range config.Repositories {
		if command != nil && *command != repo.File {
			continue
		}
		repoStatus := RepoStatus{Repo: &repo, Status: RepoKO}
		if !update {
			if existFile(filepath.Join(config.Paths.TargetDir, repo.File)) {
				fmt.Printf("  - ignoring existing command %s\n", repo.File)
				repoStatus.Status = RepoExist
				repoStatusList = append(repoStatusList, repoStatus)
				continue
			}
		}

		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo.Name)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if config.Auth.Token != "" {
			req.Header.Set("Authorization", fmt.Sprintf("token %s", config.Auth.Token))
		}
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("  - Error fetching releases for %s: %v\n", repo.Name, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("  - Non-OK HTTP status: %s for %s\n", resp.Status, repo.Name)
			continue
		}

		var release struct {
			Assets []ReleaseAsset `json:"assets"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			fmt.Printf("  - Error decoding JSON for %s: %v\n", repo.Name, err)
			continue
		}

		archList, ok := ArchEquiv[hostArch]
		if !ok {
			archList = []string{hostArch}
		}
		osList, ok := OSEquiv[hostOS]
		if !ok {
			osList = []string{hostOS}
		}

	finderloop:
		for _, asset := range release.Assets {
			assetName := strings.ToLower(asset.Name)
			for _, arch := range archList {
				if !strings.Contains(assetName, arch) {
					continue
				}
				for _, os := range osList {
					if !strings.Contains(assetName, os) {
						continue
					}
					fmt.Printf("  + identified Asset: %s (%s, %s)\n", asset.Name, arch, os)
					repoStatus.Status = RepoOK
					repoStatus.Asset = asset.Name
					repoStatus.Url = asset.BrowserDownloadURL
					repoStatus.Format = getAssetFormat(asset.Name)
					break finderloop
				}
			}
		}
		repoStatusList = append(repoStatusList, repoStatus)
	}

	fmt.Printf("[Repositories]\n")
	for _, repoStatus := range repoStatusList {
		fmt.Printf("    repository: %s ", repoStatus.Repo.Name)
		switch repoStatus.Status {
		case RepoOK:
			fmt.Println(okStyle.Render("[OK]"))
		case RepoKO:
			fmt.Println(errorStyle.Render("[XXX]"))
		case RepoExist:
			fmt.Println(warningStyle.Render("[Exist]"))
		}
	}
	// TODO What happens if not all repositories are OK?
	fmt.Printf("[Fetching]\n")
	for _, repoStatus := range repoStatusList {
		if repoStatus.Status != RepoOK {
			fmt.Printf("  %s %s\n", repoStatus.Repo.Name, warningStyle.Render("[Ignored]"))
			continue
		}
		if err := downloadFile(repoStatus.Url, repoStatus.Format, repoStatus.Repo.File, filepath.Join(config.Paths.TargetDir, repoStatus.Repo.File)); err != nil {
			fmt.Printf("    %s: %s\n", repoStatus.Repo.File, errorStyle.Render(fmt.Sprintf("[%s]", err.Error())))
			break
		}
		fmt.Printf("    %s %s\n", repoStatus.Repo.File, okStyle.Render("[Fetched]"))
	}
}

func readConfig(configPath string) (Config, error) {
	var config Config
	fileInfo, err := os.Stat(configPath)
	if err != nil {
		return config, err
	}

	if fileInfo.IsDir() {
		entries, err := os.ReadDir(configPath)
		if err != nil {
			return config, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			oneConfig, err := readOneConfig(filepath.Join(configPath, entry.Name()))
			if err != nil {
				return config, err
			}
			if err := mergo.Merge(&config, oneConfig); err != nil {
				return config, err
			}
		}
	} else {
		config, err = readOneConfig(configPath)
		if err != nil {
			return config, err
		}
	}

	return config, nil
}

func getAssetFormat(assetName string) EAssetFormat {
	if strings.HasSuffix(assetName, ".tar.gz") {
		return TargzipFormat
	}
	if strings.HasSuffix(assetName, ".tgz") {
		return TargzipFormat
	}
	if strings.HasSuffix(assetName, ".tar") {
		return TarballFormat
	}
	if strings.HasSuffix(assetName, ".zip") {
		return ZipFormat
	}
	return BinaryFormat
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		usr, err := user.Current()
		if err != nil {
			return "", err
		}
		homeDir := usr.HomeDir
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func checkTargetDir(targetDir string) error {
	info, err := os.Stat(targetDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("target directory %s is not a directory", targetDir)
	}
	tmpFile, err := os.CreateTemp(targetDir, "write_test_*")
	if err != nil {
		return fmt.Errorf("target directory %s is not writable", targetDir)
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name())

	return nil
}

func readOneConfig(configPath string) (Config, error) {
	var config Config
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return config, fmt.Errorf("error reading config file: %v", err)
	}
	return config, nil
}

func existFile(fileName string) bool {
	if _, err := os.Stat(fileName); err != nil {
		return false
	}
	return true
}

func downloadFile(url string, assetFormat EAssetFormat, fileName string, filePath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("non-OK HTTP status: %s", resp.Status)
	}

	switch assetFormat {
	case TarballFormat:
		return writeTarballFile(fileName, filePath, resp.Body)
	case TargzipFormat:
		return writeTargzipFile(fileName, filePath, resp.Body)
	case ZipFormat:
		return writeZipFile(fileName, filePath, resp.Body)
	case BinaryFormat:
		return writeBinaryFile(filePath, resp.Body)
	}
	return nil
}

func writeTarballFile(fileName string, filePath string, content io.Reader) error {
	tmpPath, err := os.MkdirTemp("/tmp", "gogo_work_*")
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	tmpFileName := filepath.Join(tmpPath, "asset.tar")
	if err := writeBinaryFile(tmpFileName, content); err != nil {
		return err
	}
	file, err := os.Open(tmpFileName)
	if err != nil {
		return err
	}
	defer file.Close()
	tarReader := tar.NewReader(file)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Name != fileName {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if err := writeBinaryFile(filePath, tarReader); err != nil {
			return err
		}
		break
	}
	return nil
}

func writeTargzipFile(fileName string, filePath string, content io.Reader) error {
	tmpPath, err := os.MkdirTemp("/tmp", "gogo_work_*")
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	tmpFileName := filepath.Join(tmpPath, "asset.tar.gz")
	if err := writeBinaryFile(tmpFileName, content); err != nil {
		return err
	}
	file, err := os.Open(tmpFileName)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Name != fileName {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if err := writeBinaryFile(filePath, tarReader); err != nil {
			return err
		}
		break
	}
	return nil
}

func writeZipFile(fileName string, filePath string, content io.Reader) error {
	tmpPath, err := os.MkdirTemp("/tmp", "gogo_work_*")
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	tmpFileName := filepath.Join(tmpPath, "asset.zip")
	if err := writeBinaryFile(tmpFileName, content); err != nil {
		return err
	}
	file, err := os.Open(tmpFileName)
	if err != nil {
		return err
	}
	defer file.Close()
	zipReader, err := zip.OpenReader(tmpFileName)
	if err != nil {
		return err
	}
	defer zipReader.Close()
	for _, file := range zipReader.File {
		if file.Name != fileName {
			continue
		}
		zipFile, err := file.Open()
		if err != nil {
			return err
		}
		defer zipFile.Close()
		if err := writeBinaryFile(filePath, zipFile); err != nil {
			return err
		}
		break
	}
	return nil
}

func writeBinaryFile(filePath string, content io.Reader) error {
	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, content); err != nil {
		return err
	}

	if err = os.Chmod(filePath, 0o755); err != nil {
		return err
	}

	return nil
}
