package config

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

type Options struct {
	DataDir    string
	ServerRoot string
	WebDir     string
	Host       string
	Port       int
}

type Config struct {
	DataDir      string `json:"dataDir"`
	DatabasePath string `json:"databasePath"`
	ServerRoot   string `json:"serverRoot"`
	WebDir       string `json:"webDir"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
}

func Load(options Options) (Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}

	dataDir := options.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(cwd, ".cliff")
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return Config{}, err
	}

	serverRoot := options.ServerRoot
	if serverRoot == "" {
		serverRoot = os.Getenv("CLIFF_SERVER_ROOT")
	}
	if serverRoot == "" {
		serverRoot = os.Getenv("CLIFF_SERVER_ROOT")
	}
	if serverRoot == "" {
		serverRoot = filepath.Join(cwd, "servers")
	}
	serverRoot, err = filepath.Abs(serverRoot)
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(serverRoot, 0o755); err != nil {
		return Config{}, err
	}

	webDir := options.WebDir
	if webDir == "" {
		webDir = "web"
	}
	if !filepath.IsAbs(webDir) {
		webDir = filepath.Join(cwd, webDir)
	}

	host := options.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := options.Port
	if port == 0 {
		port = 8080
	}

	return Config{
		DataDir:      dataDir,
		DatabasePath: filepath.Join(dataDir, "dashboard.sqlite"),
		ServerRoot:   serverRoot,
		WebDir:       webDir,
		Host:         host,
		Port:         port,
	}, nil
}

func (c Config) LocalURL() string {
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", itoa(c.Port))}).String()
}

func (c Config) LANURLs() []string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	urls := make([]string, 0, len(addresses))
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		// Skip link-local (APIPA 169.254.x.x) addresses — these are
		// self-assigned when an adapter is active but has no DHCP, so
		// they are not routable and can't be used to connect.
		if ip.IsLinkLocalUnicast() {
			continue
		}
		urls = append(urls, (&url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), itoa(c.Port))}).String())
	}
	return urls
}

func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var out [20]byte
	i := len(out)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		i--
		out[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		out[i] = '-'
	}
	return string(out[i:])
}
