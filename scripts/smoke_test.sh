#!/bin/bash

# run via sudo -E

set -eu

test_backend() {
    local os=$1
    local isolation=$2
    local backend=$3
    local prefix=$4

    extras="--os $os --isolation $isolation"
    if [ -n "$backend" ]; then
        extras="$extras --backend $backend"
    fi

    set -x
    $prefix $HOME/bin/yoloai new x . --debug --replace --force --yes --model haiku --prompt "create a file called hello in /yoloai/files" $extras
    set +x
    sleep 10

    $HOME/bin/yoloai files x get hello --force
}

# make

if [ "$(uname -s)" == "Linux" ]; then
    echo "Testing Linux host"
    test_backend linux container           docker  ""
    test_backend linux container           docker  "sudo -E"

    test_backend linux container           podman  ""
    test_backend linux container           podman  "sudo -E"

    test_backend linux container-enhanced  ""      ""
    test_backend linux container-enhanced  ""      "sudo -E"

    test_backend linux vm                  ""      "sudo -E"

    test_backend linux vm-enhanced         ""      "sudo -E"
else
    echo "Testing MacOS host"
    test_backend linux container           docker  ""
    test_backend linux container           podman  ""
    test_backend linux vm                  ""      ""
    test_backend mac   container           ""      ""
    test_backend mac   vm                  ""      ""
fi

rm -f hello

