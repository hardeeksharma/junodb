#!/bin/bash

#
# Service variables
#
name=$NAME
group=$GROUP
prefix=$PREFIX
service=$SERVICE
stage=$STAGE

# Check to see if state.log already exists
if [ -e $prefix/$name/state.log ]; then
    # Check to see if it is a fifo process
    state_log_pid=$(ps -wo pid,cmd -u $group | egrep "$prefix/$name/[s]tate\.log")
    if [ -z "$state_log_pid" ]; then
        # It is not a process
        rm $prefix/$name/state.log
    else
        # try to kill the fifo process
        kill $(echo $state_log_pid | cut -d'/' -f1)
    fi
fi

#
# Create log directory
#
if [ ! -d $prefix/$name/state-logs ]; then
        mkdir -p $prefix/$name/state-logs
fi

#
# Start the logs
#
FIFO="/usr/local/bin/fifo"
MULTILOG="/usr/local/bin/multilog s11867040 n60"

trap '' SIGHUP

echo ""
echo "Starting $name $service state log." "["`date`"]"
echo ""
statelogs=$prefix/$name/state-logs
mkdir -p $statelogs
exec $FIFO $prefix/$name/state.log | $MULTILOG $statelogs