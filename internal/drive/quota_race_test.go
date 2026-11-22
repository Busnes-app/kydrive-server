package drive_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Uploads racing for room for one: one lands, the rest are ErrQuota, on either backend.
func TestConcurrentPublishesKeepTheQuota(t *testing.T) {
	st, w, root := fixture(t) // quota 100
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		v := blob(t, root, strings.Repeat("x", 60))
		wg.Go(func() {
			_, err := st.Drive().Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: fmt.Sprintf("%d.bin", i)}, v, 0)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	landed := 0
	for err := range errs {
		switch {
		case err == nil:
			landed++
		case !errors.Is(err, drive.ErrQuota):
			t.Fatal(err)
		}
	}
	used, err := drive.QueryInt(ctx, st.Drive(), `SELECT COALESCE(SUM(v.size),0) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?`, w.ID)
	if err != nil || landed != 1 || used != 60 {
		t.Fatalf("%d landed, %d bytes used: %v", landed, used, err)
	}
}
