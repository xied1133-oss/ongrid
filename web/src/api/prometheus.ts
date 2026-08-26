import { request } from './client';

export type PrometheusLaunchInput = {
  expr: string;
  range_input?: string;
  end_input?: string;
  step_input?: string;
};

export function createPrometheusLaunch(input: PrometheusLaunchInput) {
  return request<{ url: string }>('POST', '/prometheus/launch', input);
}
