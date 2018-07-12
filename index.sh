#!/bin/bash -e
#
# Copyright 2018 Google Inc. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
set -x
go get -d "$@"
packages=($(go list "$@"))
all_packages=($(go list -f $'{{.ImportPath}}\n{{join .Deps "\\n"}}' "$@" | sort -u))
go install -i "${all_packages[@]}"
packages=($(go list -f $'{{.ImportPath}}\n{{ join .Imports "\\n" }}' "$@" | sort -u))
/gotool --corpus="GOPATH" --goroot="$(go env GOROOT)" --output=/tmp/out.kzip --continue "${packages[@]}"
/go_indexer --continue /tmp/out.kzip > /tmp/out.entries
/write_tables --experimental_beam_pipeline --entries /tmp/out.entries --out /tmp/out
