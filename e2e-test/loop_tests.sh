#!/bin/bash
i=0
while true
do
    i=$(($i+1))
    echo "===== Test round $i ====="
    if [ $# -gt 0 ]; then
        echo "Arguments: $@"
    fi
    # go test -count=1 -timeout 15m -run ^TestZoneSwitch$ election-agent/e2e-test -v --args --feature zone-test8
    E2E_TEST=y go test -count=1 -timeout 10000h -run ^TestZoneSwitch$ election-agent/e2e-test -v --args $@
    ret=$?
    if [ ! $ret -eq 0 ]; then
        echo "===== Test round  $i failed ====="
        exit 1
    fi
done
