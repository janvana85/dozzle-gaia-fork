package docker_support

import "time"

var maxDashboardClientTimeout = 3 * time.Second

func dashboardClientTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > maxDashboardClientTimeout {
		return maxDashboardClientTimeout
	}
	return timeout
}
