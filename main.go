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
	"sort"
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
	Name    string   `toml:"name"`
	File    string   `toml:"file"`
	Utils   []string `toml:"utils"`
	Comment string   `toml:"comment"`
	Tags    []string `toml:"tags"`
}

type Repositories []Repository

func (p Repositories) Len() int           { return len(p) }
func (p Repositories) Less(i, j int) bool { return p[i].File < p[j].File }
func (p Repositories) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type Config struct {
	Auth         Auth         `toml:"auth"`
	Paths        Paths        `toml:"paths"`
	Repositories Repositories `toml:"repositories"`
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
	VERSION = "0.0.4"

	ArchEquiv = map[string][]string{
		"amd64": {"amd64", "x86_64", ""},
		"arm64": {"arm64", "amd64", "x86_64", ""},
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
		fmt.Println("  tags            display all tags")
		fmt.Println("  fetch [command] fetch one or all commands")
		fmt.Println("  fetch [repo]    fetch command from repository")
		fmt.Println("                  (can be author/repo or full GitHub URL)")
		fmt.Println("\nFlags:")
		fmt.Println("  -config <config-file> path to a configuration file or directory")
		fmt.Println("  -update               update commands if already installed")
		fmt.Println("  -tags                 filter by tags")
		os.Exit(1)
	}
	command := os.Args[1]
	args := os.Args[2:]

	listCmd := flag.NewFlagSet("list", flag.ExitOnError)
	listConfigPath := listCmd.String("config", "config.toml", "Path to the TOML configuration file")
	listTags := listCmd.String("tags", "", "Filter by tags")
	tagsCmd := flag.NewFlagSet("tags", flag.ExitOnError)
	tagsConfigPath := tagsCmd.String("config", "config.toml", "Path to the TOML configuration file")
	fetchCmd := flag.NewFlagSet("fetch", flag.ExitOnError)
	fetchConfigPath := fetchCmd.String("config", "config.toml", "Path to the TOML configuration file")
	fetchUpdate := fetchCmd.Bool("update", false, "Update commands if already installed")
	fetchTags := fetchCmd.String("tags", "", "Filter by tags")

	switch command {
	case "list":
		listCmd.Parse(args)
		doList(*listConfigPath, expandTags(*listTags))
	case "tags":
		tagsCmd.Parse(args)
		doTags(*tagsConfigPath)
	case "fetch":
		if strings.HasPrefix(args[0], "-") {
			fetchCmd.Parse(args)
			doFetch(*fetchConfigPath, *fetchUpdate, nil, expandTags(*fetchTags))
		} else {
			fetchCmd.Parse(args[1:])
			doFetch(*fetchConfigPath, *fetchUpdate, &args[0], expandTags(*fetchTags))
		}
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func expandTags(tags string) []string {
	if tags == "" {
		return []string{}
	}
	return strings.Split(tags, ",")
}

func doList(configPath string, tags []string) {
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
	t.Headers("Binary", "Description", "Tags")

	for _, repo := range config.Repositories {
		if len(tags) > 0 && !containsTag(repo.Tags, tags) {
			continue
		}
		t.Row(repo.File, repo.Comment, strings.Join(repo.Tags, ", "))
	}
	fmt.Println(t)
}

func doTags(configPath string) {
	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		os.Exit(1)
	}

	tagSet := make(map[string]int)
	for _, repo := range config.Repositories {
		for _, tag := range repo.Tags {
			if _, ok := tagSet[tag]; !ok {
				tagSet[tag] = 0
			}
			tagSet[tag] += 1
		}
	}

	type tagcnt struct {
		Tag string
		Cnt int
	}
	var tagSlice []tagcnt
	for tag, cnt := range tagSet {
		tagSlice = append(tagSlice, tagcnt{Tag: tag, Cnt: cnt})
	}
	sort.Slice(tagSlice, func(i, j int) bool {
		return tagSlice[i].Tag < tagSlice[j].Tag
	})

	t := table.New().
		Border(lipgloss.NormalBorder()).
		StyleFunc(
			func(_, col int) lipgloss.Style {
				switch col {
				case 1:
					return lipgloss.NewStyle().Padding(0, 1).Align(lipgloss.Right)
				default:
					return lipgloss.NewStyle().Padding(0, 1)
				}
			},
		).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("99")))
	t.Headers("Tag", "Repos")

	for _, tc := range tagSlice {
		t.Row(tc.Tag, fmt.Sprintf("%d", tc.Cnt))
	}
	fmt.Println(t)
}

func doFetch(configPath string, update bool, command *string, tags []string) {
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

	var checkedRepos *Repositories

	var bits []string
	if command != nil {
		bits = strings.Split(*command, "/")
	}
	if len(bits) > 1 {
		// This is a repo
		var directRepo Repository
		if bits[0] == "https:" {
			directRepo.Name = strings.Join(bits[3:5], "/")
			directRepo.File = bits[4]
		} else {
			directRepo.Name = strings.Join(bits[0:2], "/")
			directRepo.File = bits[1]
		}
		*command = directRepo.File
		checkedRepos = &Repositories{directRepo}
	} else {
		checkedRepos = &config.Repositories
	}

	repoStatusList := []RepoStatus{}

	fmt.Printf("[Preflight]\n")
	for _, repo := range *checkedRepos {
		if command != nil && *command != repo.File {
			continue
		}
		if len(tags) > 0 && !containsTag(repo.Tags, tags) {
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
		if err := downloadFile(repoStatus.Url, repoStatus.Format, repoStatus.Repo.File, repoStatus.Repo.Utils, config.Paths.TargetDir); err != nil {
			fmt.Printf("  %s: %s\n", repoStatus.Repo.File, errorStyle.Render(fmt.Sprintf("[%s]", err.Error())))
			break
		}
		fmt.Printf("  %s %s\n", repoStatus.Repo.Name, okStyle.Render("[Fetched]"))
	}
}

func containsTag(repoTags []string, tags []string) bool {
	for _, tag := range tags {
		for _, repoTag := range repoTags {
			if tag == repoTag {
				return true
			}
		}
	}
	return false
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
			fmt.Printf("Config merging %s\n", entry.Name())
			oneConfig, err := readOneConfig(filepath.Join(configPath, entry.Name()))
			if err != nil {
				return config, err
			}
			if err := mergo.Merge(&config, oneConfig, mergo.WithAppendSlice); err != nil {
				return config, err
			}
		}
	} else {
		config, err = readOneConfig(configPath)
		if err != nil {
			return config, err
		}
	}
	sort.Sort(Repositories(config.Repositories))

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

func downloadFile(url string, assetFormat EAssetFormat, fileName string, utils []string, targetDir string) error {
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
		return writeTarballFile(fileName, utils, targetDir, resp.Body)
	case TargzipFormat:
		return writeTargzipFile(fileName, utils, targetDir, resp.Body)
	case ZipFormat:
		return writeZipFile(fileName, utils, targetDir, resp.Body)
	case BinaryFormat:
		filePath := filepath.Join(targetDir, fileName)
		return writeBinaryFile(filePath, resp.Body)
	}
	return nil
}

func writeTarballFile(fileName string, utils []string, targetDir string, content io.Reader) error {
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
		if header.Typeflag != tar.TypeReg {
			continue
		}
		var proceed *string
		if filepath.Base(header.Name) == fileName {
			proceed = &fileName
		} else {
			for _, util := range utils {
				if filepath.Base(header.Name) == util {
					proceed = &util
					break
				}
			}
		}
		if proceed == nil {
			continue
		}
		filePath := filepath.Join(targetDir, *proceed)
		if err := writeBinaryFile(filePath, tarReader); err != nil {
			return err
		}
		if len(utils) == 0 {
			break
		}
	}
	return nil
}

func writeTargzipFile(fileName string, utils []string, targetDir string, content io.Reader) error {
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
		if header.Typeflag != tar.TypeReg {
			continue
		}
		var proceed *string
		if filepath.Base(header.Name) == fileName {
			proceed = &fileName
		} else {
			for _, util := range utils {
				if filepath.Base(header.Name) == util {
					proceed = &util
					break
				}
			}
		}
		if proceed == nil {
			continue
		}
		filePath := filepath.Join(targetDir, *proceed)
		if err := writeBinaryFile(filePath, tarReader); err != nil {
			return err
		}
		if len(utils) == 0 {
			break
		}
	}
	return nil
}

func writeZipFile(fileName string, utils []string, targetDir string, content io.Reader) error {
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
		var proceed *string
		if filepath.Base(file.Name) == fileName {
			proceed = &fileName
		} else {
			for _, util := range utils {
				if filepath.Base(file.Name) == util {
					proceed = &util
					break
				}
			}
		}
		if proceed == nil {
			continue
		}
		zipFile, err := file.Open()
		if err != nil {
			return err
		}
		defer zipFile.Close()
		filePath := filepath.Join(targetDir, *proceed)
		if err := writeBinaryFile(filePath, zipFile); err != nil {
			return err
		}
		if len(utils) == 0 {
			break
		}
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
