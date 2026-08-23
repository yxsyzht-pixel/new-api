import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm, type Resolver } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { z } from "zod";

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
import { api } from "@/lib/api";

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
  baseUrl: z.string(),
  appId: z.string(),
  appSecret: z.string(),
  requireDirectory: z.boolean(),
});

type Values = z.infer<typeof schema>;
export type StaffDirectoryDefaults = Values;

export function StaffDirectorySection({
  defaultValues,
}: {
  defaultValues: StaffDirectoryDefaults;
}) {
  const { t } = useTranslation();
  const updateOption = useUpdateOption();
  const [refreshing, setRefreshing] = useState(false);
  const [saved, setSaved] = useState<Values>(defaultValues);

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues,
  });
  const { isDirty, isSubmitting } = form.formState;

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = [];
    const push = (key: string, next: unknown, previous: unknown) => {
      if (next !== previous) updates.push({ key, value: String(next) });
    };

    push("staff_directory_setting.enabled", values.enabled, saved.enabled);
    push("staff_directory_setting.base_url", values.baseUrl, saved.baseUrl);
    push("staff_directory_setting.app_id", values.appId, saved.appId);
    // The secret never comes back down, so an untouched box means "keep it".
    if (values.appSecret !== "") {
      updates.push({
        key: "staff_directory_setting.app_secret",
        value: values.appSecret,
      });
    }
    push(
      "staff_directory_setting.require_directory",
      values.requireDirectory,
      saved.requireDirectory,
    );

    if (updates.length === 0) {
      toast.info(t("No changes to save"));
      return;
    }
    for (const update of updates) {
      await updateOption.mutateAsync(update);
    }
    const nowSaved = { ...values, appSecret: "" };
    setSaved(nowSaved);
    form.reset(nowSaved);
  }

  async function onRefresh() {
    setRefreshing(true);
    try {
      const { data } = await api.post<{
        success: boolean;
        message: string;
        data?: { total?: number };
      }>("/api/token/staff-directory/refresh", {});
      if (data?.success) {
        toast.success(
          t("Directory refreshed: {{total}} people", {
            total: data.data?.total ?? 0,
          }),
        );
      } else {
        toast.error(data?.message ?? t("Request failed"));
      }
    } catch (error) {
      toast.error(String(error));
    } finally {
      setRefreshing(false);
    }
  }

  return (
    <SettingsSection title={t("Staff directory")}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete="off">
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel={t("Save directory settings")}
          />

          <FormField
            control={form.control}
            name="enabled"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t("Pick staff IDs from the directory")}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      "A staff ID decides whose transcript a conversation joins and whose memory it becomes. Reading the company directory turns that field into a choice instead of something typed from memory.",
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
            name="baseUrl"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Data service address")}</FormLabel>
                <FormControl>
                  <Input {...field} placeholder="https://datas.vyxsy.com" />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="appId"
            render={({ field }) => (
              <FormItem>
                <FormLabel>appId</FormLabel>
                <FormControl>
                  <Input {...field} autoComplete="off" />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="appSecret"
            render={({ field }) => (
              <FormItem>
                <FormLabel>appSecret</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type="password"
                    autoComplete="new-password"
                  />
                </FormControl>
                <FormDescription>
                  {t("Leave empty to keep the stored one.")}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={refreshing}
              onClick={onRefresh}
            >
              {refreshing ? t("Refreshing…") : t("Test and refresh now")}
            </Button>
          </div>

          <FormField
            control={form.control}
            name="requireDirectory"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Refuse unknown staff IDs")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Anyone granted “Type a staff ID freehand” can still write one the directory does not list.",
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
        </SettingsForm>
      </Form>
    </SettingsSection>
  );
}
