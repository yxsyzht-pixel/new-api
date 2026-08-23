/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { SystemBehaviorSection } from "../general/system-behavior-section";
import { EmailSettingsSection } from "../integrations/email-settings-section";
import { MonitoringSettingsSection } from "../integrations/monitoring-settings-section";
import { WorkerSettingsSection } from "../integrations/worker-settings-section";
import { LogSettingsSection } from "../maintenance/log-settings-section";
import { PerformanceSection } from "../maintenance/performance-section";
import { UpdateCheckerSection } from "../maintenance/update-checker-section";
import type { OperationsSettings } from "../types";
import { ChatRecordSection } from "./chat-record-section";
import { StaffDirectorySection } from "./staff-directory-section";

import { createSectionRegistry } from "../utils/section-registry";

// Byte-valued settings are shown in megabytes; the option itself stays in bytes.
const toMb = (value: number | undefined, fallback: number) =>
  Math.max(1, Math.round((value ?? fallback) / (1024 * 1024)));

const OPERATIONS_SECTIONS = [
  {
    id: "behavior",
    titleKey: "System Behavior",
    build: (settings: OperationsSettings) => (
      <SystemBehaviorSection
        defaultValues={{
          DefaultCollapseSidebar: settings.DefaultCollapseSidebar,
          DemoSiteEnabled: settings.DemoSiteEnabled,
          SelfUseModeEnabled: settings.SelfUseModeEnabled,
        }}
      />
    ),
  },
  {
    id: "alerts",
    titleKey: "Monitoring & Alerts",
    build: (settings: OperationsSettings) => (
      <MonitoringSettingsSection
        defaultValues={{
          QuotaRemindThreshold: settings.QuotaRemindThreshold,
          "perf_metrics_setting.enabled":
            settings["perf_metrics_setting.enabled"] ?? true,
          "perf_metrics_setting.flush_interval":
            settings["perf_metrics_setting.flush_interval"] ?? 5,
          "perf_metrics_setting.bucket_time":
            settings["perf_metrics_setting.bucket_time"] ?? "hour",
          "perf_metrics_setting.retention_days":
            settings["perf_metrics_setting.retention_days"] ?? 0,
        }}
      />
    ),
  },
  {
    id: "email",
    titleKey: "SMTP Email",
    build: (settings: OperationsSettings) => (
      <EmailSettingsSection
        defaultValues={{
          SMTPServer: settings.SMTPServer,
          SMTPPort: settings.SMTPPort,
          SMTPAccount: settings.SMTPAccount,
          SMTPFrom: settings.SMTPFrom,
          SMTPToken: settings.SMTPToken,
          SMTPSSLEnabled: settings.SMTPSSLEnabled,
          SMTPStartTLSEnabled: settings.SMTPStartTLSEnabled,
          SMTPInsecureSkipVerify: settings.SMTPInsecureSkipVerify,
          SMTPForceAuthLogin: settings.SMTPForceAuthLogin,
        }}
      />
    ),
  },
  {
    id: "worker",
    titleKey: "Worker Proxy",
    build: (settings: OperationsSettings) => (
      <WorkerSettingsSection
        defaultValues={{
          WorkerUrl: settings.WorkerUrl,
          WorkerValidKey: settings.WorkerValidKey,
          WorkerAllowHttpImageRequestEnabled:
            settings.WorkerAllowHttpImageRequestEnabled,
        }}
      />
    ),
  },
  {
    id: "logs",
    titleKey: "Log Maintenance",
    build: (settings: OperationsSettings) => (
      <LogSettingsSection
        defaultEnabled={Boolean(settings.LogConsumeEnabled)}
      />
    ),
  },
  {
    id: "performance",
    titleKey: "Performance",
    build: (settings: OperationsSettings) => (
      <PerformanceSection
        defaultValues={{
          "performance_setting.disk_cache_enabled":
            settings["performance_setting.disk_cache_enabled"] ?? false,
          "performance_setting.disk_cache_threshold_mb":
            settings["performance_setting.disk_cache_threshold_mb"] ?? 10,
          "performance_setting.disk_cache_max_size_mb":
            settings["performance_setting.disk_cache_max_size_mb"] ?? 1024,
          "performance_setting.disk_cache_path":
            settings["performance_setting.disk_cache_path"] ?? "",
          "performance_setting.monitor_enabled":
            settings["performance_setting.monitor_enabled"] ?? false,
          "performance_setting.monitor_cpu_threshold":
            settings["performance_setting.monitor_cpu_threshold"] ?? 90,
          "performance_setting.monitor_memory_threshold":
            settings["performance_setting.monitor_memory_threshold"] ?? 90,
          "performance_setting.monitor_disk_threshold":
            settings["performance_setting.monitor_disk_threshold"] ?? 95,
        }}
      />
    ),
  },
  {
    id: "chat-record",
    titleKey: "Chat transcript recording",
    build: (settings: OperationsSettings) => (
      <ChatRecordSection
        defaultValues={{
          enabled: settings["chat_record_setting.enabled"] ?? false,
          host: settings["chat_record_setting.host"] ?? "",
          port: settings["chat_record_setting.port"] || "5432",
          database: settings["chat_record_setting.database"] ?? "",
          user: settings["chat_record_setting.user"] ?? "",
          // Never sent to the page; an empty box means "keep the saved one".
          password: "",
          sslMode: settings["chat_record_setting.ssl_mode"] || "disable",
          storeFiles: settings["chat_record_setting.store_files"] ?? true,
          fileRoot:
            settings["chat_record_setting.file_root"] ||
            "data/chat-record-files",
          maxFileMb: toMb(
            settings["chat_record_setting.max_file_bytes"],
            20 * 1024 * 1024,
          ),
          autoMessagePatterns:
            settings["chat_record_setting.auto_message_patterns"] ?? "",
          automationModels:
            settings["chat_record_setting.automation_models"] ?? "",
          memoryEnabled:
            settings["chat_record_setting.memory_enabled"] ?? false,
          memoryBaseUrl: settings["chat_record_setting.memory_base_url"] ?? "",
          memoryApiKey: "",
          memoryWorkspace:
            settings["chat_record_setting.memory_workspace"] ?? "yxsy",
          memoryPeerTemplate:
            settings["chat_record_setting.memory_peer_template"] ??
            "{staff_id}",
          memoryAssistantPeer:
            settings["chat_record_setting.memory_assistant_peer"] ??
            "{agent}-{staff_id}",
          memorySessionMode:
            settings["chat_record_setting.memory_session_mode"] ?? "person",
          memoryMinChars: settings["chat_record_setting.memory_min_chars"] ?? 4,
          queueSize: settings["chat_record_setting.queue_size"] ?? 4096,
          workers: settings["chat_record_setting.workers"] ?? 4,
          maxContentChars:
            settings["chat_record_setting.max_content_chars"] ?? 32000,
          maxQueuedMb: toMb(
            settings["chat_record_setting.max_queued_bytes"],
            64 * 1024 * 1024,
          ),
        }}
      />
    ),
  },
  {
    id: "staff-directory",
    titleKey: "Staff directory",
    build: (settings: OperationsSettings) => (
      <StaffDirectorySection
        defaultValues={{
          enabled: settings["staff_directory_setting.enabled"] ?? false,
          baseUrl:
            settings["staff_directory_setting.base_url"] ??
            "https://datas.vyxsy.com",
          appId: settings["staff_directory_setting.app_id"] ?? "",
          appSecret: "",
          requireDirectory:
            settings["staff_directory_setting.require_directory"] ?? true,
        }}
      />
    ),
  },
  {
    id: "update-checker",
    titleKey: "System maintenance",
    build: (
      _settings: OperationsSettings,
      currentVersion?: string | null,
      startTime?: number | null,
    ) => (
      <UpdateCheckerSection
        currentVersion={currentVersion}
        startTime={startTime}
      />
    ),
  },
] as const;

export type OperationsSectionId = (typeof OPERATIONS_SECTIONS)[number]["id"];

const operationsRegistry = createSectionRegistry<
  OperationsSectionId,
  OperationsSettings,
  [string | null | undefined, number | null | undefined]
>({
  sections: OPERATIONS_SECTIONS,
  defaultSection: "behavior",
  basePath: "/system-settings/operations",
  urlStyle: "path",
});

export const OPERATIONS_SECTION_IDS = operationsRegistry.sectionIds;
export const OPERATIONS_DEFAULT_SECTION = operationsRegistry.defaultSection;
export const getOperationsSectionNavItems =
  operationsRegistry.getSectionNavItems;
export const getOperationsSectionContent = operationsRegistry.getSectionContent;
export const getOperationsSectionMeta = operationsRegistry.getSectionMeta;
