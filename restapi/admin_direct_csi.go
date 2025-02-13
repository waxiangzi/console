// This file is part of MinIO Console Server
// Copyright (c) 2021 MinIO, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package restapi

import (
	"context"
	"sort"
	"strings"

	"github.com/minio/console/cluster"

	"github.com/go-openapi/runtime/middleware"
	"github.com/minio/console/models"
	"github.com/minio/console/restapi/operations"
	"github.com/minio/console/restapi/operations/admin_api"
	directv1alpha1apis "github.com/minio/direct-csi/pkg/apis/direct.csi.min.io/v1alpha1"
	directv1alpha1 "github.com/minio/direct-csi/pkg/clientset/typed/direct.csi.min.io/v1alpha1"
	"github.com/minio/direct-csi/pkg/sys"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func registerDirectCSIHandlers(api *operations.ConsoleAPI) {
	api.AdminAPIGetDirectCSIDriveListHandler = admin_api.GetDirectCSIDriveListHandlerFunc(func(params admin_api.GetDirectCSIDriveListParams, session *models.Principal) middleware.Responder {
		resp, err := getDirectCSIDrivesListResponse(session)
		if err != nil {
			return admin_api.NewGetDirectCSIDriveListDefault(int(err.Code)).WithPayload(err)
		}
		return admin_api.NewGetDirectCSIDriveListOK().WithPayload(resp)
	})
	api.AdminAPIGetDirectCSIVolumeListHandler = admin_api.GetDirectCSIVolumeListHandlerFunc(func(params admin_api.GetDirectCSIVolumeListParams, session *models.Principal) middleware.Responder {
		resp, err := getDirectCSIVolumesListResponse(session)
		if err != nil {
			return admin_api.NewGetDirectCSIVolumeListDefault(int(err.Code)).WithPayload(err)
		}
		return admin_api.NewGetDirectCSIVolumeListOK().WithPayload(resp)
	})
}

// getDirectCSIVolumesList returns direct-csi drives
func getDirectCSIDriveList(ctx context.Context, clientset directv1alpha1.DirectV1alpha1Interface) (*models.GetDirectCSIDriveListResponse, error) {
	drivesList, err := clientset.DirectCSIDrives().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	volList, err := clientset.DirectCSIVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	res := &models.GetDirectCSIDriveListResponse{}

	// implementation same as direct-csi `drives ls` command
	driveName := func(val string) string {
		dr := strings.ReplaceAll(val, sys.DirectCSIDevRoot+"/", "")
		dr = strings.ReplaceAll(dr, sys.HostDevRoot+"/", "")
		return strings.ReplaceAll(dr, sys.DirectCSIPartitionInfix, "")
	}
	drivesSorted := drivesList.Items
	// sort by nodename, path and status
	sort.SliceStable(drivesSorted, func(i, j int) bool {
		d1 := drivesSorted[i]
		d2 := drivesSorted[j]

		if v := strings.Compare(d1.Status.NodeName, d2.Status.NodeName); v != 0 {
			return v < 0
		}

		if v := strings.Compare(d1.Status.Path, d2.Status.Path); v != 0 {
			return v < 0
		}

		return strings.Compare(string(d1.Status.DriveStatus), string(d2.Status.DriveStatus)) < 0
	})
	for _, d := range drivesSorted {
		var volumes int64
		for _, v := range volList.Items {
			if v.Status.Drive == d.Name {
				volumes++
			}
		}
		msg := ""
		dr := func(val string) string {
			dr := driveName(val)
			for _, c := range d.Status.Conditions {
				if c.Type == string(directv1alpha1apis.DirectCSIDriveConditionInitialized) {
					if c.Status != metav1.ConditionTrue {
						msg = c.Message
						continue
					}
				}
				if c.Type == string(directv1alpha1apis.DirectCSIDriveConditionOwned) {
					if c.Status != metav1.ConditionTrue {
						msg = c.Message
						continue
					}
				}
			}
			return strings.ReplaceAll("/dev/"+dr, sys.DirectCSIPartitionInfix, "")
		}(d.Status.Path)
		drStatus := d.Status.DriveStatus
		if msg != "" {
			drStatus = drStatus + "*"
			msg = strings.ReplaceAll(msg, d.Name, "")
			msg = strings.ReplaceAll(msg, sys.DirectCSIDevRoot, "/dev")
			msg = strings.ReplaceAll(msg, sys.DirectCSIPartitionInfix, "")
			msg = strings.Split(msg, "\n")[0]
		}

		driveInfo := &models.DirectCSIDriveInfo{
			Drive:     dr,
			Capacity:  d.Status.TotalCapacity,
			Allocated: d.Status.AllocatedCapacity,
			Node:      d.Status.NodeName,
			Status:    string(drStatus),
			Message:   msg,
			Volumes:   volumes,
		}
		res.Drives = append(res.Drives, driveInfo)
	}

	return res, nil
}

func getDirectCSIDrivesListResponse(session *models.Principal) (*models.GetDirectCSIDriveListResponse, *models.Error) {
	ctx := context.Background()
	client, err := cluster.DirectCSIClient(session.STSSessionToken)
	if err != nil {
		return nil, prepareError(err)
	}

	drives, err := getDirectCSIDriveList(ctx, client.DirectV1alpha1())
	if err != nil {
		return nil, prepareError(err)
	}
	return drives, nil
}

// getDirectCSIVolumesList returns direct-csi volumes
func getDirectCSIVolumesList(ctx context.Context, clientset directv1alpha1.DirectV1alpha1Interface) (*models.GetDirectCSIVolumeListResponse, error) {
	drivesList, err := clientset.DirectCSIDrives().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	volList, err := clientset.DirectCSIVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// implementation same as direct-csi `volumes ls` command
	drivePaths := map[string]string{}
	driveName := func(val string) string {
		dr := strings.ReplaceAll(val, sys.DirectCSIDevRoot+"/", "")
		return strings.ReplaceAll(dr, sys.HostDevRoot+"/", "")
	}

	for _, d := range drivesList.Items {
		drivePaths[d.Name] = driveName(d.Status.Path)
	}
	var volumes []*models.DirectCSIVolumeInfo
	for _, v := range volList.Items {
		vol := &models.DirectCSIVolumeInfo{
			Volume:   v.Name,
			Capacity: v.Status.TotalCapacity,
			Drive:    driveName(drivePaths[v.Status.Drive]),
			Node:     v.Status.NodeName,
		}

		volumes = append(volumes, vol)
	}

	res := &models.GetDirectCSIVolumeListResponse{
		Volumes: volumes,
	}
	return res, nil
}

func getDirectCSIVolumesListResponse(session *models.Principal) (*models.GetDirectCSIVolumeListResponse, *models.Error) {
	ctx := context.Background()
	client, err := cluster.DirectCSIClient(session.STSSessionToken)
	if err != nil {
		return nil, prepareError(err)
	}

	volumes, err := getDirectCSIVolumesList(ctx, client.DirectV1alpha1())
	if err != nil {
		return nil, prepareError(err)
	}
	return volumes, nil
}
