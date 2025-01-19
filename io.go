package tunneld

import "io"

func copy(closer chan error, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	closer <- err // connection is closed, send signal to stop proxy
}
