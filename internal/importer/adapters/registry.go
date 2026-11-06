package adapters

import "github.com/pestit/retain/internal/importer"

var registered = []importer.Adapter{
	importer.RetainAdapter{},
	CSVAdapter{},
}

// All returns the registered import adapters in detection order.
func All() []importer.Adapter {
	return append([]importer.Adapter(nil), registered...)
}
