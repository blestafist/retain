/* global self, URL */

function readPayload(event) {
  try {
    return event.data ? event.data.json() : {};
  } catch {
    return {};
  }
}

self.addEventListener('push', (event) => {
  const payload = readPayload(event);

  event.waitUntil(self.registration.showNotification(
    typeof payload.title === 'string' ? payload.title : 'Retain',
    {
      body: typeof payload.body === 'string' ? payload.body : 'You have cards to review.',
      icon: '/pwa.svg',
      badge: '/pwa.svg',
      tag: 'retain-daily-reminder',
      data: { url: '/study' },
    },
  ));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = new URL('/study', self.location.origin).href;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of windows) {
      if (new URL(client.url).origin === self.location.origin) {
        await client.navigate(target);
        await client.focus();
        return;
      }
    }
    await self.clients.openWindow(target);
  })());
});
