// prod.go contains production functions that should not be expanded during test statement analysis.
package sampleproj

// Double is a production function defined in a non-test file.
func Double(val int) int {
	return val * 2
}
