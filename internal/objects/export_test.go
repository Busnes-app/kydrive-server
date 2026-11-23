package objects

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// SetPartSizeForTest sets the multipart part size and a hook run after each part, until the
// test ends.
func SetPartSizeForTest(t interface{ Cleanup(func()) }, size int64, after func(int32)) {
	oldSize, oldDone := partSize, partDone
	partSize, partDone = size, after
	t.Cleanup(func() { partSize, partDone = oldSize, oldDone })
}

// PendingUploadsForTest counts the unfinished multipart uploads under prefix.
func PendingUploadsForTest(ctx context.Context, b *Bucket, prefix string) (int, error) {
	out, err := b.c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(b.bucket), Prefix: aws.String(prefix)})
	if err != nil {
		return 0, err
	}
	return len(out.Uploads), nil
}

// CreateBucketForTest makes the test bucket on a fresh S3-compatible server.
func CreateBucketForTest(ctx context.Context, b *Bucket) error {
	_, err := b.c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b.bucket)})
	var ae smithy.APIError
	if errors.As(err, &ae) && (ae.ErrorCode() == "BucketAlreadyOwnedByYou" || ae.ErrorCode() == "BucketAlreadyExists") {
		return nil
	}
	return err
}
