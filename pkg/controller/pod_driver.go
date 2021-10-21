/*
Copyright 2021 Juicedata Inc

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

package controller

import (
	"context"
	"fmt"
	k8sMount "k8s.io/utils/mount"
	"os"

	"github.com/juicedata/juicefs-csi-driver/pkg/juicefs/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PodDriver struct {
	Client   client.Client
	handlers map[podStatus]podHandler
	Mounter  util.MountInter
}

func NewPodDriver(client client.Client) *PodDriver {
	driver := &PodDriver{
		Client:   client,
		handlers: map[podStatus]podHandler{},
		Mounter: k8sMount.New(""),
	}
	driver.handlers[podReady] = driver.podReadyHandler
	driver.handlers[podError] = driver.podErrorHandler
	driver.handlers[podRunning] = driver.podRunningHandler
	driver.handlers[podDeleted] = driver.podDeletedHandler
	return driver
}

type podHandler func(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error)
type podStatus string

const (
	podReady   podStatus = "podReady"
	podError   podStatus = "podError"
	podDeleted podStatus = "podDeleted"
	podRunning podStatus = "podRunning"
)

func (p *PodDriver) Run(ctx context.Context, current *corev1.Pod) (reconcile.Result, error) {
	return p.handlers[p.getPodStatus(current)](ctx, current)
}

func (p *PodDriver) getPodStatus(pod *corev1.Pod) podStatus {
	if pod == nil {
		return podError
	}
	if pod.DeletionTimestamp != nil {
		return podDeleted
	}
	if util.IsPodError(pod) {
		return podError
	}
	if util.PodReadyStatus(pod) {
		return podReady
	}
	return podRunning
}

func (p *PodDriver) podErrorHandler(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	// todo
	// delete the err pod, if the pod has targetPath, create the pod ,after delete finish
	if pod == nil {
		return reconcile.Result{}, nil
	}
	klog.V(5).Infof("Get pod %s in namespace %s is err status, deleting thd pod.", pod.Name, pod.Namespace)
	if err := p.Client.Delete(ctx, pod); err != nil {
		klog.V(5).Infof("delete po:%s err:%v", pod.Name, err)
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, nil
}

func (p *PodDriver) podDeletedHandler(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	klog.V(5).Infof("Get pod %s in namespace %s is to be deleted.", pod.Name, pod.Namespace)
	if !util.ContainsString(pod.GetFinalizers(), config.Finalizer) {
		// do nothing
		return reconcile.Result{}, nil
	}
	// todo
	klog.V(5).Infof("Remove finalizer of pod %s namespace %s", pod.Name, pod.Namespace)
	controllerutil.RemoveFinalizer(pod, config.Finalizer)
	if err := p.Client.Update(ctx, pod); err != nil {
		klog.Errorf("Update pod err:%v", err)
		return reconcile.Result{}, err
	}
	// do recovery
	klog.V(6).Infof("Annotations:%v", pod.Annotations)
	if pod.Annotations == nil {
		return reconcile.Result{}, nil
	}
	var targets = make([]string, 0)
	volumeId, ok := pod.Annotations[config.VolumeIdKey]
	if !ok {
		return reconcile.Result{}, nil
	}
	for k, v := range pod.Annotations {
		if k == util.GetReferenceKey(v) {
			targets = append(targets, v)
			// umount target
			//for {
			//	//klog.V(5).Infof("umount target:%s\n", v)
			//	mntErr := p.Mounter.Unmount(v) // may mount repeated
			//	if mntErr != nil {
			//		klog.V(5).Infof("umount get err:%v", mntErr)
			//		break
			//	}
			//}
		}
	}
	if len(targets) == 0 {
		return reconcile.Result{}, nil
	}
	sourcePath := fmt.Sprintf("%s/%s", config.PodMountBase, volumeId)
	klog.Infof("start umount :%s", sourcePath)
	if err := p.Mounter.Unmount(sourcePath); err != nil {
		klog.Errorf("umount %s err:%v\n", sourcePath, err)
		//return reconcile.Result{}, nil
	}
	// create
	klog.V(5).Infof("pod targetPath not empty, need create thd pod:%s", pod.Name)
	// todo check pod not exists
	controllerutil.AddFinalizer(pod, config.Finalizer)
	pod.ResourceVersion = ""
	err := p.Client.Create(ctx, pod)
	if err != nil {
		klog.Errorf("create pod:%s err:%v", pod.Name, err)
	}
	return reconcile.Result{}, nil
}

func (p *PodDriver) podReadyHandler(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	// bind target
	// do recovery

	if pod.Annotations == nil {
		return reconcile.Result{}, nil
	}
	//var targets = make([]string, 0)
	volumeId, ok := pod.Annotations[config.VolumeIdKey]
	if !ok {
		return reconcile.Result{}, nil
	}
	sourcePath := fmt.Sprintf("%s/%s/%s", config.PodMountBase, volumeId, volumeId)
	mountOption := []string{"bind"}
	for k, v := range pod.Annotations {
		if k == util.GetReferenceKey(v) {
			//targets = append(targets, v)
			cmd := fmt.Sprintf("start exec cmd: mount -o bind %s %s \n", sourcePath, v)
			// check target should do recover
			_, err := os.Stat(v)
			if err == nil {
				mounted, err2 := util.IsMounted(v, p.Mounter)
				if err2 != nil {
					klog.Errorf("check v is IsMounted err:%v\n", err2)
				}
				if !mounted { // only mounted target do recovery
					klog.V(5).Infof("target not mounted:%s, don't need do recover", v)
					continue
				}
			}else if os.IsNotExist(err) {
				klog.V(5).Infof("target:%s not exists , just return", v)
				continue
			}
			// can not umount , after umount ,the container target don't recovery
			//}else {
			//	for {
			//		klog.V(5).Infof("umount target:%s\n", v)
			//		mntErr := p.Mounter.Unmount(v) // may mount repeated
			//		if mntErr != nil {
			//			klog.V(5).Infof("umount get err:%v, start bind target", mntErr)
			//			break
			//		}
			//	}
			//}
			klog.V(5).Infof("Get pod %s in namespace %s is ready, %s", pod.Name, pod.Namespace, cmd)
			if err := p.Mounter.Mount(sourcePath, v, "none", mountOption); err != nil {
				klog.Errorf("exec cmd: mount -o bind %s %s err:%v", sourcePath, v, err)
			}
		}
	}
	return reconcile.Result{}, nil
}

func (p *PodDriver) podRunningHandler(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	// requeue
	return reconcile.Result{Requeue: true}, nil
}
