<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from 'vue-sonner'
import { Settings, Bell, Loader2 } from 'lucide-vue-next'
import { usersService, organizationService } from '@/services/api'

const isSubmitting = ref(false)
const isLoading = ref(true)

// General Settings
const generalSettings = ref({
  organization_name: 'My Organization',
  default_timezone: 'UTC',
  date_format: 'YYYY-MM-DD',
  mask_phone_numbers: false
})

// Notification Settings
const notificationSettings = ref({
  email_notifications: true,
  new_message_alerts: true,
  campaign_updates: true
})

onMounted(async () => {
  try {
    const [orgResponse, userResponse] = await Promise.all([
      organizationService.getSettings(),
      usersService.me()
    ])

    // Organization settings
    const orgData = orgResponse.data.data || orgResponse.data
    if (orgData) {
      generalSettings.value = {
        organization_name: orgData.name || 'My Organization',
        default_timezone: orgData.settings?.timezone || 'UTC',
        date_format: orgData.settings?.date_format || 'YYYY-MM-DD',
        mask_phone_numbers: orgData.settings?.mask_phone_numbers || false
      }
    }

    // User notification settings
    const user = userResponse.data.data || userResponse.data
    if (user.settings) {
      notificationSettings.value = {
        email_notifications: user.settings.email_notifications ?? true,
        new_message_alerts: user.settings.new_message_alerts ?? true,
        campaign_updates: user.settings.campaign_updates ?? true
      }
    }
  } catch (error) {
    console.error('Failed to load settings:', error)
  } finally {
    isLoading.value = false
  }
})

async function saveGeneralSettings() {
  isSubmitting.value = true
  try {
    await organizationService.updateSettings({
      name: generalSettings.value.organization_name,
      timezone: generalSettings.value.default_timezone,
      date_format: generalSettings.value.date_format,
      mask_phone_numbers: generalSettings.value.mask_phone_numbers
    })
    toast.success('General settings saved')
  } catch (error) {
    toast.error('Failed to save settings')
  } finally {
    isSubmitting.value = false
  }
}

async function saveNotificationSettings() {
  isSubmitting.value = true
  try {
    await usersService.updateSettings({
      email_notifications: notificationSettings.value.email_notifications,
      new_message_alerts: notificationSettings.value.new_message_alerts,
      campaign_updates: notificationSettings.value.campaign_updates
    })
    toast.success('Notification settings saved')
  } catch (error) {
    toast.error('Failed to save notification settings')
  } finally {
    isSubmitting.value = false
  }
}
</script>

<template>
  <div class="flex flex-col h-full">
    <!-- Header -->
    <header class="border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div class="flex h-16 items-center px-6">
        <Settings class="h-5 w-5 mr-3" />
        <div class="flex-1">
          <h1 class="text-xl font-semibold">Settings</h1>
          <p class="text-sm text-muted-foreground">Manage your organization settings</p>
        </div>
      </div>
    </header>

    <!-- Content -->
    <ScrollArea class="flex-1">
      <div class="p-6 space-y-4 max-w-4xl mx-auto">
        <Tabs default-value="general" class="w-full">
          <TabsList class="grid w-full grid-cols-2 mb-6">
            <TabsTrigger value="general">
              <Settings class="h-4 w-4 mr-2" />
              General
            </TabsTrigger>
            <TabsTrigger value="notifications">
              <Bell class="h-4 w-4 mr-2" />
              Notifications
            </TabsTrigger>
          </TabsList>

          <!-- General Settings Tab -->
          <TabsContent value="general">
            <Card>
              <CardHeader>
                <CardTitle>General Settings</CardTitle>
                <CardDescription>Basic organization and display settings</CardDescription>
              </CardHeader>
              <CardContent class="space-y-4">
                <div class="space-y-2">
                  <Label for="org_name">Organization Name</Label>
                  <Input
                    id="org_name"
                    v-model="generalSettings.organization_name"
                    placeholder="Your Organization"
                  />
                </div>
                <div class="grid grid-cols-2 gap-4">
                  <div class="space-y-2">
                    <Label for="timezone">Default Timezone</Label>
                    <Select v-model="generalSettings.default_timezone">
                      <SelectTrigger>
                        <SelectValue placeholder="Select timezone" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="UTC">UTC</SelectItem>
                        <SelectItem value="America/New_York">Eastern Time</SelectItem>
                        <SelectItem value="America/Los_Angeles">Pacific Time</SelectItem>
                        <SelectItem value="Europe/London">London</SelectItem>
                        <SelectItem value="Asia/Tokyo">Tokyo</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="space-y-2">
                    <Label for="date_format">Date Format</Label>
                    <Select v-model="generalSettings.date_format">
                      <SelectTrigger>
                        <SelectValue placeholder="Select format" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="YYYY-MM-DD">YYYY-MM-DD</SelectItem>
                        <SelectItem value="DD/MM/YYYY">DD/MM/YYYY</SelectItem>
                        <SelectItem value="MM/DD/YYYY">MM/DD/YYYY</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>
                <Separator />
                <div class="flex items-center justify-between">
                  <div>
                    <p class="font-medium">Mask Phone Numbers</p>
                    <p class="text-sm text-muted-foreground">Hide phone numbers showing only last 4 digits</p>
                  </div>
                  <Switch
                    :checked="generalSettings.mask_phone_numbers"
                    @update:checked="generalSettings.mask_phone_numbers = $event"
                  />
                </div>
                <div class="flex justify-end">
                  <Button variant="outline" size="sm" @click="saveGeneralSettings" :disabled="isSubmitting">
                    <Loader2 v-if="isSubmitting" class="mr-2 h-4 w-4 animate-spin" />
                    Save Changes
                  </Button>
                </div>
              </CardContent>
            </Card>
          </TabsContent>

          <!-- Notification Settings Tab -->
          <TabsContent value="notifications">
            <Card>
              <CardHeader>
                <CardTitle>Notifications</CardTitle>
                <CardDescription>Manage how you receive notifications</CardDescription>
              </CardHeader>
              <CardContent class="space-y-4">
                <div class="flex items-center justify-between">
                  <div>
                    <p class="font-medium">Email Notifications</p>
                    <p class="text-sm text-muted-foreground">Receive important updates via email</p>
                  </div>
                  <Switch
                    :checked="notificationSettings.email_notifications"
                    @update:checked="notificationSettings.email_notifications = $event"
                  />
                </div>
                <Separator />
                <div class="flex items-center justify-between">
                  <div>
                    <p class="font-medium">New Message Alerts</p>
                    <p class="text-sm text-muted-foreground">Get notified when new messages arrive</p>
                  </div>
                  <Switch
                    :checked="notificationSettings.new_message_alerts"
                    @update:checked="notificationSettings.new_message_alerts = $event"
                  />
                </div>
                <Separator />
                <div class="flex items-center justify-between">
                  <div>
                    <p class="font-medium">Campaign Updates</p>
                    <p class="text-sm text-muted-foreground">Receive campaign status notifications</p>
                  </div>
                  <Switch
                    :checked="notificationSettings.campaign_updates"
                    @update:checked="notificationSettings.campaign_updates = $event"
                  />
                </div>
                <div class="flex justify-end pt-4">
                  <Button variant="outline" size="sm" @click="saveNotificationSettings" :disabled="isSubmitting">
                    <Loader2 v-if="isSubmitting" class="mr-2 h-4 w-4 animate-spin" />
                    Save Changes
                  </Button>
                </div>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </ScrollArea>
  </div>
</template>
