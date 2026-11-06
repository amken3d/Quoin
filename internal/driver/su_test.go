package driver

import "github.com/amken3d/quoin/internal/stack"

func readSUForTest(path string) (map[string]int64, error) { return stack.ReadSU([]string{path}) }
