export function canvasAPIKeyManagementPath(openCreate = false): string {
    const query = new URLSearchParams({ returnTo: "/studio" });
    if (openCreate) query.set("create", "1");
    return `/keys?${query.toString()}`;
}
