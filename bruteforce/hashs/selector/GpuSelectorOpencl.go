// +build opencl

package selector

import (
	"github.com/ngirot/BruteForce/bruteforce/hashs/hashers"
)

func BuildGpuHasherMap() (map[string]func() hashers.Hasher, error) {
	var hasherMap = make(map[string]func() hashers.Hasher)

	hasherMap["sha256"] = func() hashers.Hasher { return hashers.NewHasherGpuSha256() }

	return hasherMap, nil
}
