package objects

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// CreateBucketForTest makes the test bucket on a fresh S3-compatible server.
func CreateBucketForTest(ctx context.Context, b *Bucket) error {
	_, err := b.c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b.bucket)})
	var ae smithy.APIError
	if errors.As(err, &ae) && (ae.ErrorCode() == "BucketAlreadyOwnedByYou" || ae.ErrorCode() == "BucketAlreadyExists") {
		return nil
	}
	return err
}
