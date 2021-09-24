
import os
import time
import subprocess

def main():
    # get targetPath
    volumeId = os.getenv("volumeId")
    if volumeId == "":
        print("env volumeId cann't be empty")
        return
    source_path = "/jfs/{volId}/{volId}".format(volId=volumeId)
    for i in range(30):
        time.sleep(2)
        # wait source_path mount, timeout 1 minute
        po = subprocess.Popen("ls %s"% source_path, stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=True)
        ls_res = po.stderr.read().decode("utf-8")
        if ls_res == "":
            break
        print("ls %s res:" % source_path, ls_res)
        if i == 30:
            print("recover time out")
            return
    mount_tmp = "%s/mount" % volumeId
    mount_res = os.popen("grep %s /proc/mounts |awk '{print $2}'" % mount_tmp).readlines()
    print(mount_res)
    tmp_map = {}
    for i in mount_res:
        i = i.strip()
        # ls: cannot access mount: Transport endpoint is not connected
        # only conncet err do mount
        if not mount_tmp in i:
            print("get err mount_path:%s" % i)
            continue
        if tmp_map.get(i):
            print("get target_path %s repeated, just skip" % i)
            continue
        # ls_res = subprocess.Popen .popen("ls %s" % i).readlines()
        po = subprocess.Popen("ls %s"% i, stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=True)

        # ls_res = po.stdout.read().decode("utf-8")
        ls_res = po.stderr.read().decode("utf-8")
        print("---ls_res:%s---"% ls_res)
        if not "Transport endpoint is not connected" in ls_res:
            print("targetPath %s do not need mount" % i)
            continue
        cmd = "mount --bind /jfs/{volId}/{volId} {target}".format(volId=volumeId, target=i)
        print("exec mount_cmd:", cmd)
        mount_res = os.popen(cmd).read()
        print("mount_res:", mount_res)
        tmp_map[i] = True


if __name__ == "__main__":
    main()