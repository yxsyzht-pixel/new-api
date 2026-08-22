import { zodResolver } from "@hookform/resolvers/zod";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useForm, type Resolver } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { z } from "zod";

import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from "../components/settings-form-layout";
import { SettingsPageFormActions } from "../components/settings-page-context";
import { SettingsSection } from "../components/settings-section";
import { useUpdateOption } from "../hooks/use-update-option";

const schema = z.object({
  enabled: z.boolean(),
  dsn: z.string(),
  queueSize: z.coerce.number().int().min(1),
  workers: z.coerce.number().int().min(1),
  maxContentChars: z.coerce.number().int().min(1),
  maxQueuedMb: z.coerce.number().int().min(1),
});

type Values = z.infer<typeof schema>;

type ChatRecordStatus = {
  running: boolean;
  queued: number;
  capacity: number;
  written?: number;
  dropped?: number;
  failed?: number;
  dsn_configured: boolean;
  dsn_masked: string;
};

export type ChatRecordDefaults = {
  enabled: boolean;
  dsn: string;
  queueSize: number;
  workers: number;
  maxContentChars: number;
};

export function ChatRecordSection({
  defaultValues,
}: {
  defaultValues: ChatRecordDefaults;
}) {
  const { t } = useTranslation();
  const updateOption = useUpdateOption();
  const [initializing, setInitializing] = useState(false);
  const [testing, setTesting] = useState(false);

  // The saved address never leaves the gateway in full — it carries a database
  // password — so the page shows a redacted copy and treats an empty field as
  // "leave it as it is".
  const { data: status, refetch: refetchStatus } = useQuery({
    queryKey: ["chat-record-status"],
    queryFn: async () => {
      const { data } = await api.get<{ data: ChatRecordStatus }>(
        "/api/option/chat_record/status",
      );
      return data.data;
    },
    refetchInterval: 15_000,
  });

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues,
  });

  const { isDirty, isSubmitting } = form.formState;

  // The address is sent with the request so the operator can check it before
  // committing to it; an empty field falls back to whatever is already saved.
  async function callWithDsn(path: string, setBusy: (busy: boolean) => void) {
    setBusy(true);
    try {
      const { data } = await api.post<{ success: boolean; message: string }>(
        path,
        { dsn: form.getValues("dsn") },
      );
      if (data?.success) {
        toast.success(data.message);
        void refetchStatus();
      } else toast.error(data?.message ?? t("Request failed"));
    } catch (error) {
      toast.error(String(error));
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = [];
    const push = (key: string, next: unknown, previous: unknown) => {
      if (next !== previous) updates.push({ key, value: String(next) });
    };

    push("chat_record_setting.enabled", values.enabled, defaultValues.enabled);
    if (values.dsn !== "") {
      updates.push({ key: "chat_record_setting.dsn", value: values.dsn });
    }
    push(
      "chat_record_setting.queue_size",
      values.queueSize,
      defaultValues.queueSize,
    );
    push("chat_record_setting.workers", values.workers, defaultValues.workers);
    push(
      "chat_record_setting.max_content_chars",
      values.maxContentChars,
      defaultValues.maxContentChars,
    );

    if (updates.length === 0) {
      toast.info(t("No changes to save"));
      return;
    }
    for (const update of updates) {
      await updateOption.mutateAsync(update);
    }
    form.reset(values);
  }

  return (
    <SettingsSection title={t("Chat transcript recording")}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete="off">
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel={t("Save transcript settings")}
          />

          <FormField
            control={form.control}
            name="enabled"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Record chat transcripts")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Stores each turn’s user message and model reply in a separate database. Writing happens on a queue the relay never waits on: if the store falls behind, transcripts are dropped rather than requests being slowed.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="dsn"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Transcript database address")}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    autoComplete="off"
                    placeholder={
                      status?.dsn_configured
                        ? status.dsn_masked
                        : "postgres://user:password@host:5432/dbname?sslmode=disable"
                    }
                  />
                </FormControl>
                <FormDescription>
                  {status?.dsn_configured
                    ? t(
                        "An address is already saved (shown without its password). Leave this empty to keep it.",
                      )
                    : t(
                        "A PostgreSQL database of your own. Use “Initialise” once to create the table and its indexes.",
                      )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={testing}
              onClick={() =>
                callWithDsn("/api/option/chat_record/test", setTesting)
              }
            >
              {testing ? t("Testing…") : t("Test connection")}
            </Button>
            <Button
              type="button"
              disabled={initializing}
              onClick={() =>
                callWithDsn("/api/option/chat_record/init", setInitializing)
              }
            >
              {initializing ? t("Initialising…") : t("Initialise database")}
            </Button>
          </div>

          {status ? (
            <p className="text-muted-foreground text-sm">
              {status.running
                ? t(
                    "Writer running · {{queued}}/{{capacity}} queued · {{written}} written · {{dropped}} dropped · {{failed}} failed",
                    {
                      queued: status.queued,
                      capacity: status.capacity,
                      written: status.written ?? 0,
                      dropped: status.dropped ?? 0,
                      failed: status.failed ?? 0,
                    },
                  )
                : t("Writer stopped")}
            </p>
          ) : null}

          <FormField
            control={form.control}
            name="queueSize"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Queue size")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "How many finished turns may wait to be written. A full queue drops new ones so no request ever waits.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="workers"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Writer count")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="maxContentChars"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Characters kept per message")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "Longer messages are cut at this length. A turn replays the whole conversation, so without a cap one row could hold megabytes.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  );
}
