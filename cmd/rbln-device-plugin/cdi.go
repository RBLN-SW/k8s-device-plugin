/*
Copyright 2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdiparser "tags.cncf.io/container-device-interface/pkg/parser"

	"github.com/RBLN-SW/k8s-device-plugin/pkg/consts"
)

type CDIHandler struct {
	root string
}

func NewCDIHandler(root string) (*CDIHandler, error) {
	return &CDIHandler{
		root: root,
	}, nil
}

func (cdi *CDIHandler) Initialize() error {
	return cdi.cleanupTransientSpecs()
}

func (cdi *CDIHandler) cleanupTransientSpecs() error {
	entries, err := os.ReadDir(cdi.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	prefix := fmt.Sprintf("%s-%s_", cdi.vendor(), cdi.class())
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(cdi.root, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

func (cdi *CDIHandler) RuntimeAnnotations() (map[string]string, error) {
	return cdiapi.UpdateAnnotations(
		nil,
		cdi.vendor(),
		cdi.class(),
		[]string{cdiparser.QualifiedName(cdi.vendor(), cdi.class(), consts.BaseCDIDevice)},
	)
}

func (cdi *CDIHandler) RDSAnnotations(annotations map[string]string) (map[string]string, error) {
	return cdiapi.UpdateAnnotations(
		annotations,
		cdi.vendor(),
		consts.RDSCDIClass,
		[]string{cdiparser.QualifiedName(cdi.vendor(), consts.RDSCDIClass, consts.RDSCDIAllDevice)},
	)
}

func (cdi *CDIHandler) class() string {
	return consts.CDIClass
}

func (cdi *CDIHandler) vendor() string {
	return consts.CDIVendor
}
