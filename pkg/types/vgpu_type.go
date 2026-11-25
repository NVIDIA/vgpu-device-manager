/*
 * Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package types

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// AttributeMediaExtensions holds the string representation for the +me MIG profile attribute.
	AttributeMediaExtensions = "ME"
	// AttributeNoMediaExtensions holds the string representation for the -me MIG profile attribute.
	AttributeNoMediaExtensions = "NOME"
	// AttributeMediaExtensionsAll holds the string representation for the +me.all MIG profile attribute.
	AttributeMediaExtensionsAll = "MEALL"
	// AttributeGraphics holds the string representation for the +gfx MIG profile attribute.
	AttributeGraphics = "GFX"

	// timeSlicedRegex represents the format for a time-sliced, vGPU type name.
	// It embeds the GPU type, framebuffer size in GB, and a letter representing the 'series'.
	// Note: The framebuffer size can be '0' to represent 512MB (i.e. M60-0Q).
	timeSlicedRegex = "^(?P<GPU>[A-Z0-9]+(-([a-zA-Z]+))*)-(?P<GB>0|[1-9][0-9]*)(?P<S>A|B|C|Q)$"
	// migBackedRegex represents the format for a MIG-backed, vGPU type name.
	// In addition to embedding all of the fields from 'timeSlicedRegex', it also
	// contains the number of GPU instances and any additional attributes (i.e. media extensions).
	migBackedRegex = "^(?P<GPU>[A-Z0-9]+)-(?P<G>[1-9])-(?P<GB>0|[1-9][0-9]*)(?P<S>A|B|C|Q)(?P<ATTR>(ME|NOME|MEALL|GFX))?$"
)

// VGPUType represents a specific vGPU type.
// Time-sliced vGPU types appear as <gpu>-<gb><series>.
// MIG-backed vGPU types appear as <gpu>-<g>-<gb><series>[ME, NOME, MEALL, GFX].
// Examples include "A100-40C", "A100D-80C", "A100-1-5C", "A100-1-5CME", "DC-1-24CGFX" etc.
type VGPUType struct {
	GPU  string
	G    int
	GB   int
	S    Series
	Attr []string
}

// ParseVGPUType converts a string representation of a VGPUType into an object.
func ParseVGPUType(s string) (*VGPUType, error) {
	var gpu string
	var g, gb int
	var series Series
	var attr []string

	if len(s) == 0 {
		return nil, fmt.Errorf("empty vGPU type string")
	}

	captureGroups := parseRegex(timeSlicedRegex, s)
	if len(captureGroups) == 0 {
		captureGroups = parseRegex(migBackedRegex, s)
	}

	if len(captureGroups) == 0 {
		return nil, fmt.Errorf("malformed vGPU type string '%s'", s)
	}

	gpu = captureGroups["GPU"]

	gbStr := captureGroups["GB"]
	gb, err := strconv.Atoi(gbStr)
	if err != nil {
		return nil, fmt.Errorf("malformed number for framebuffer size '%s'", gbStr)
	}

	seriesStr := captureGroups["S"]
	series = Series(seriesStr[0])
	if !series.IsValid() {
		return nil, fmt.Errorf("invalid series '%c'", series)
	}

	gStr, ok := captureGroups["G"]
	if ok {
		// MIG-backed
		g, err = strconv.Atoi(gStr)
		if err != nil {
			return nil, fmt.Errorf("malformed number for GPU instances '%s'", gStr)
		}
		attribute, ok := captureGroups["ATTR"]
		if ok {
			attr = append(attr, attribute)
		}
	}

	v := &VGPUType{
		GPU:  gpu,
		GB:   gb,
		S:    series,
		G:    g,
		Attr: attr,
	}
	return v, nil
}

func parseRegex(re, s string) map[string]string {
	var r = regexp.MustCompile(re)
	match := r.FindStringSubmatch(s)

	captureGroups := make(map[string]string)
	for i, name := range r.SubexpNames() {
		if i > 0 && i <= len(match) {
			captureGroups[name] = match[i]
		}
	}

	return captureGroups
}

func (v VGPUType) String() string {
	if v.G == 0 {
		return fmt.Sprintf("%s-%d%c", v.GPU, v.GB, v.S)
	}

	var suffix string
	if len(v.Attr) > 0 {
		suffix = strings.Join(v.Attr, "")
	}
	return fmt.Sprintf("%s-%d-%d%c%s", v.GPU, v.G, v.GB, v.S, suffix)
}
