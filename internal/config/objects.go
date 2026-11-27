package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/objects"
)

// ObjectStorageConfig is the hosted object destination. Secrets never serialize.
type ObjectStorageConfig struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string `json:"-"`
	PathStyle bool
}

func (o ObjectStorageConfig) Configured() bool { return o.Endpoint != "" }
func (o ObjectStorageConfig) S3() objects.S3Config {
	return objects.S3Config{Endpoint: o.Endpoint, Region: o.Region, Bucket: o.Bucket, AccessKey: o.AccessKey, SecretKey: o.SecretKey, PathStyle: o.PathStyle}
}

const MaxCells = 32

var cellSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func ParseCells(raw string) ([]string, error) {
	var cells []string
	for _, c := range strings.Split(raw, ",") {
		c = strings.TrimSpace(c)
		if !cellSlug.MatchString(c) || slices.Contains(cells, c) {
			return nil, errors.New("KY_CELLS: 1 to 32 distinct home-region slugs (^[a-z][a-z0-9-]{1,31}$), comma-separated")
		}
		cells = append(cells, c)
	}
	if len(cells) > MaxCells {
		return nil, errors.New("KY_CELLS: at most 32 cells")
	}
	slices.Sort(cells)
	return cells, nil
}
func hostedStorage(multi bool) ([]string, ObjectStorageConfig, error) {
	var o ObjectStorageConfig
	rawCells := getEnv("KY_CELLS", "")
	configured := false
	for _, name := range []string{"ENDPOINT", "BUCKET", "REGION", "ACCESS_KEY", "SECRET_KEY", "SECRET_KEY_FILE", "PATH_STYLE"} {
		configured = configured || getEnv("KYDRIVE_S3_"+name, "") != ""
	}
	if getEnv("KY_REGION", "") != "" {
		return nil, o, errors.New("KY_REGION is replaced by KY_CELLS")
	}
	if !multi {
		if rawCells != "" || configured {
			return nil, o, errors.New("KY_CELLS and KYDRIVE_S3_* require KYDRIVE_TENANCY=multi")
		}
		return nil, o, nil
	}
	// Preserve the release gate before checking the hosted-only settings.
	if !multiTenantBuild {
		return nil, o, errors.New("KYDRIVE_TENANCY=multi is not available in this release")
	}
	cells, err := ParseCells(rawCells)
	if err != nil {
		return nil, o, err
	}
	if getEnv("KY_DB_DRIVER", "sqlite") != "postgres" && getEnv("KY_DB_DRIVER", "") != "postgresql" && getEnv("KY_DB_DRIVER", "") != "pgx" {
		return nil, o, errors.New("KYDRIVE_TENANCY=multi requires PostgreSQL")
	}
	o = ObjectStorageConfig{Endpoint: getEnv("KYDRIVE_S3_ENDPOINT", ""), Region: getEnv("KYDRIVE_S3_REGION", "us-east-1"), Bucket: getEnv("KYDRIVE_S3_BUCKET", ""), AccessKey: getEnv("KYDRIVE_S3_ACCESS_KEY", "")}
	o.PathStyle, err = getEnvBool("KYDRIVE_S3_PATH_STYLE", true)
	if err != nil {
		return nil, o, err
	}
	o.SecretKey = os.Getenv("KYDRIVE_S3_SECRET_KEY")
	if file := getEnv("KYDRIVE_S3_SECRET_KEY_FILE", ""); file != "" {
		secret, err := applysetup.Secret(file)
		if err != nil {
			return nil, o, fmt.Errorf("KYDRIVE_S3_SECRET_KEY_FILE: %w", err)
		}
		if o.SecretKey != "" && o.SecretKey != secret {
			return nil, o, errors.New("KYDRIVE_S3_SECRET_KEY: conflicting secret sources")
		}
		o.SecretKey = secret
	}
	if err := objects.ValidateS3(o.S3()); err != nil {
		return nil, o, fmt.Errorf("KYDRIVE_S3_*: %w", err)
	}
	return cells, o, nil
}
