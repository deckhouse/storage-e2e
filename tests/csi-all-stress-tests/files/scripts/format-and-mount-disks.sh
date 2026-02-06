#!/bin/bash
set -e

# Wait for disks to appear after hot-plug (up to 60 seconds)
echo "Waiting for hot-plugged disks to appear..."

# Find new unformatted disks - supports both VirtIO (vd*) and SCSI (sd*) disks
# Excludes boot disk (sda) and small disks (< 1GB)
find_new_disks() {
    lsblk -bdpno NAME,SIZE,TYPE | grep 'disk' | while read disk size type; do
        # Skip small disks (< 1GB = 1073741824 bytes) - these are likely metadata/config disks
        if [ "$size" -lt 1073741824 ]; then
            continue
        fi
        
        # Skip sda (boot disk)
        if [ "$disk" = "/dev/sda" ]; then
            continue
        fi
        
        # Check if disk has no partitions and no filesystem
        if ! lsblk -no FSTYPE "$disk" 2>/dev/null | grep -q .; then
            echo "$disk"
        fi
    done | head -2
}

max_attempts=12
attempt=0
new_disks=""
disk_count=0

while [ $attempt -lt $max_attempts ]; do
    new_disks=$(find_new_disks)
    disk_count=$(echo "$new_disks" | grep -c '/dev/' || true)
    
    if [ "$disk_count" -ge 2 ]; then
        echo "Found $disk_count new unformatted disk(s) after $attempt attempts"
        break
    fi
    
    attempt=$((attempt + 1))
    echo "Attempt $attempt/$max_attempts: Found $disk_count disks, waiting 5s..."
    sleep 5
done

if [ "$disk_count" -lt 2 ]; then
    echo "Error: Expected 2 new disks, found $disk_count after $max_attempts attempts"
    echo "Current block devices:"
    lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT
    exit 1
fi

# Convert to array
disk1=$(echo "$new_disks" | head -1)
disk2=$(echo "$new_disks" | tail -1)

echo "Disk 1: $disk1 -> /mnt/nfs"
echo "Disk 2: $disk2 -> /mnt/minio"

# Format disks with ext4
echo "Formatting $disk1 with ext4..."
mkfs.ext4 -F "$disk1"

echo "Formatting $disk2 with ext4..."
mkfs.ext4 -F "$disk2"

# Create mount points
mkdir -p /mnt/nfs /mnt/minio

# Mount disks
echo "Mounting $disk1 to /mnt/nfs..."
mount "$disk1" /mnt/nfs

echo "Mounting $disk2 to /mnt/minio..."
mount "$disk2" /mnt/minio

# Add to fstab for persistence
disk1_uuid=$(blkid -s UUID -o value "$disk1")
disk2_uuid=$(blkid -s UUID -o value "$disk2")

echo "UUID=$disk1_uuid /mnt/nfs ext4 defaults 0 0" >> /etc/fstab
echo "UUID=$disk2_uuid /mnt/minio ext4 defaults 0 0" >> /etc/fstab

# Verify mounts
echo "Verifying mounts..."
df -h /mnt/nfs /mnt/minio

echo "Done! Disks formatted and mounted successfully."
