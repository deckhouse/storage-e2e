# HPE 3PAR SAN Diagnostic Guide

## Current System Status Analysis

Based on your `showversion` and `showsys` output:

**System Information:**
- Model: HPE 3PAR 8400
- Serial: CZ3811SC9P
- Version: 3.3.1 (MU3) with patches P50-P128
- Nodes: 2 nodes (Master: Node 0)
- Cluster LED: **Off** (this is normal when system is healthy)

**Capacity Status:**
- Total Capacity: 14,639,104 MB (~14.6 TB)
- Allocated: 4,809,728 MB (~4.8 TB)
- Free: 9,829,376 MB (~9.8 TB)
- Failed: 0 MB ✅ (Good - no failed capacity)

**Initial Assessment:** The system appears healthy based on basic metrics (no failed capacity, proper node count).

## Diagnostic Commands to Run

Run these commands in order to get a complete health picture:

### 1. System Health and Status
```bash
# Overall system health
showsys -health

# Detailed system status
showsys -d

# System statistics
showsys -space

# Check for any alerts or events
showeventlog -min 60  # Last 60 minutes
showeventlog -alert   # All alerts
```

### 2. Node Status
```bash
# Check node health
shownode -d

# Node statistics
shownode -stat

# Check if all nodes are up and healthy
shownode -state
```

### 3. Disk and Cage Status
```bash
# Check all physical disks
showpd -d

# Check for failed or degraded disks
showpd -failed
showpd -degraded

# Check disk cage status
showcage -d

# Check disk statistics
showpd -stat
```

### 4. CPG (Common Provisioning Group) Status
```bash
# List all CPGs
showcpg -d

# Check CPG space usage
showcpg -space

# Check CPG growth history
showcpg -hist
```

### 5. Volume Status
```bash
# List all volumes
showvv -d

# Check volume statistics
showvv -stat

# Check for degraded volumes
showvv -degraded

# Check volume space usage
showvv -space
```

### 6. Network and Port Status
```bash
# Check FC/iSCSI ports
showport -d

# Check port statistics
showport -stat

# Check for failed ports
showport -failed
```

### 7. Service Processor (SP) Status
```bash
# Check service processor status
showsp -d

# Check SP network connectivity
showsp -net
```

### 8. Performance and Statistics
```bash
# System statistics
statcpu
statport
statpd
statvv
statrcopy

# Real-time statistics (run for 30-60 seconds)
statcpu -iter 1 -rw 1
statport -iter 1 -rw 1
```

### 9. Replication Status (if configured)
```bash
# Check remote copy status
showrcopy -d

# Check remote copy groups
showrcopygroup -d
```

### 10. Check for Known Issues
```bash
# Check for any service issues
showservice -d

# Check license status
showlicense

# Check firmware versions
showversion -d
```

## Key Health Indicators to Check

### ✅ Healthy Indicators:
- All nodes show "UP" status
- No failed physical disks (showpd -failed returns empty)
- No degraded volumes (showvv -degraded returns empty)
- All ports show "Ready" status
- No critical alerts in event log
- Cluster LED is Off (normal)
- Failed capacity is 0 MB

### ⚠️ Warning Indicators:
- Any node showing "DOWN" or "DEGRADED"
- Failed or degraded physical disks
- Degraded volumes
- Ports showing "FAILED" or "DEGRADED"
- Critical alerts in event log
- High latency in statistics
- Unusual capacity growth patterns

### 🔴 Critical Issues:
- Multiple nodes down
- Multiple failed disks
- System unresponsive
- Data unavailability
- Replication failures (if configured)

## Quick Health Check Script

Save this as a script and run it:

```bash
#!/bin/bash
echo "=== 3PAR Health Check ==="
echo ""
echo "1. System Health:"
showsys -health
echo ""
echo "2. Node Status:"
shownode -d
echo ""
echo "3. Failed Disks:"
showpd -failed
echo ""
echo "4. Degraded Volumes:"
showvv -degraded
echo ""
echo "5. Recent Alerts (last hour):"
showeventlog -min 60 -alert
echo ""
echo "6. Port Status:"
showport -d | grep -E "(Port|State|Status)"
echo ""
echo "=== Health Check Complete ==="
```

## Next Steps

1. **Run the diagnostic commands** above and collect output
2. **Review the event log** for any recent errors or warnings
3. **Check performance statistics** if you're experiencing performance issues
4. **Verify backups/replication** if configured
5. **Document baseline metrics** for future comparison

## Common Issues and Solutions

### Issue: High Latency
- Check port statistics: `statport`
- Check disk statistics: `statpd`
- Review CPG layout and disk types

### Issue: Capacity Warnings
- Check CPG growth: `showcpg -hist`
- Review volume space: `showvv -space`
- Consider expanding or cleaning up unused volumes

### Issue: Node Degradation
- Check node status: `shownode -d`
- Review event log: `showeventlog`
- May require node replacement or service call

### Issue: Disk Failures
- Check failed disks: `showpd -failed`
- Verify spare disks are available
- Plan for disk replacement

## Support Information

If issues are found:
- Collect all diagnostic output
- Note the exact error messages
- Check HPE support portal for known issues
- Contact HPE support with system serial: CZ3811SC9P

