package objects

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

func TestS3PurgeErasesVersionsAndDeleteMarkers(t *testing.T) {
	if os.Getenv("KY_TEST_S3_ENDPOINT") == "" {
		t.Skip("requires KY_TEST_S3_*")
	}
	ctx := context.Background()
	cfg := S3Config{Endpoint: os.Getenv("KY_TEST_S3_ENDPOINT"), Bucket: "drive-versions-" + uuid.NewString(), Region: "us-east-1", AccessKey: os.Getenv("KY_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("KY_TEST_S3_SECRET_KEY"), PathStyle: true}
	b, err := NewBucket(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatal(err)
	}
	prefix := "cells/e2e-1/orgs/" + uuid.NewString() + "/"
	t.Cleanup(func() {
		if err := b.PurgePrefix(ctx, prefix); err != nil {
			t.Error(err)
		}
		if _, err := b.c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
			t.Error(err)
		}
	})
	if _, err := b.c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(cfg.Bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"search/", "blobs/", "staging/session/"} {
		st := b.Store(prefix + kind)
		for _, body := range []string{"first", "second"} {
			w, err := st.Create(ctx)
			if err != nil {
				t.Fatal(err)
			}
			w.Write([]byte(body))
			if err := w.Publish("object"); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Remove(ctx, "object"); err != nil {
			t.Fatal(err)
		}
	}
	before, err := b.c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(cfg.Bucket), Prefix: aws.String(prefix)})
	if err != nil || len(before.Versions) < 6 || len(before.DeleteMarkers) < 3 {
		t.Fatal("fixture lacks versioned objects and markers", err)
	}
	if err := b.PurgePrefix(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	after, err := b.c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(cfg.Bucket), Prefix: aws.String(prefix)})
	if err != nil || len(after.Versions) != 0 || len(after.DeleteMarkers) != 0 {
		t.Fatal("versions survived org erasure", err)
	}
	if err := b.PurgePrefix(ctx, strings.TrimSuffix(prefix, "/")); err == nil {
		t.Fatal("accepted non-directory prefix")
	}
}
