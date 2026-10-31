"use client";

// Members: roster, role changes and invitations.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Label, Select } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface Member { user_id: string; name: string; email: string; role: string }
interface Invitation { id: string; email: string; role: string; status: string; token?: string; expires_at: string }

export default function MembersPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [inviteOpen, setInviteOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inviteToken, setInviteToken] = useState<string | null>(null);

  const members = useQuery({
    queryKey: ["members"],
    queryFn: () => api<{ items: Member[] }>(path("members")),
  });
  const invitations = useQuery({
    queryKey: ["invitations"],
    queryFn: () => api<{ items: Invitation[] }>(path("invitations")),
  });

  const changeRole = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      api(path("members", userId), { method: "PATCH", body: { role } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["members"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Role change failed"),
  });

  const remove = useMutation({
    mutationFn: (userId: string) => api(path("members", userId), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["members"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Removal failed"),
  });

  const createInvite = useMutation({
    mutationFn: (body: { email: string; role: string }) =>
      api<{ token: string }>(path("invitations"), { method: "POST", body }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["invitations"] });
      setInviteToken(data.token);
      setInviteOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Invitation failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Members"
        description="Roster, roles and invitations"
        actions={<Button onClick={() => setInviteOpen(true)}>Invite member</Button>}
      />
      <PageBody className="space-y-6">
        {error ? <ErrorState message={error} /> : null}
        {inviteToken ? (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            Invitation token (share with the invitee — no email transport locally):
            <span className="ml-1 font-mono">{inviteToken}</span>
            <button className="ml-2 text-primary underline" onClick={() => setInviteToken(null)}>dismiss</button>
          </div>
        ) : null}

        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Member</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(members.data?.items ?? []).map((member) => (
                <TableRow key={member.user_id}>
                  <TableCell>
                    <p className="font-medium">{member.name}</p>
                    <p className="text-xs text-muted-foreground">{member.email}</p>
                  </TableCell>
                  <TableCell><StatusBadge status={member.role} /></TableCell>
                  <TableCell className="text-right">
                    {member.role !== "owner" ? (
                      <>
                        <Select
                          className="inline-block w-32"
                          value={member.role}
                          onChange={(e) => changeRole.mutate({ userId: member.user_id, role: e.target.value })}
                        >
                          <option value="admin">admin</option>
                          <option value="developer">developer</option>
                          <option value="operator">operator</option>
                          <option value="auditor">auditor</option>
                          <option value="member">member</option>
                        </Select>{" "}
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => {
                            if (window.confirm(`Remove ${member.email} from the organization?`)) {
                              remove.mutate(member.user_id);
                            }
                          }}
                        >
                          Remove
                        </Button>
                      </>
                    ) : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Open invitations</CardTitle>
          </CardHeader>
          <CardContent className="px-0 pb-0">
            {(invitations.data?.items ?? []).length === 0 ? (
              <div className="px-4 pb-4"><EmptyState title="No open invitations" /></div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Email</TableHead>
                    <TableHead>Role</TableHead>
                    <TableHead>Expires</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(invitations.data?.items ?? []).map((invitation) => (
                    <TableRow key={invitation.id}>
                      <TableCell>{invitation.email}</TableCell>
                      <TableCell>{invitation.role}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDateTime(invitation.expires_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </PageBody>

      <Dialog open={inviteOpen} onClose={() => setInviteOpen(false)}>
        <DialogTitle>Invite member</DialogTitle>
        <DialogContent>
          <form
            id="invite-form"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await createInvite
                .mutateAsync({ email: String(form.get("email")), role: String(form.get("role")) })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="invite-email">Email</Label>
              <Input id="invite-email" name="email" type="email" required />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="invite-role">Role</Label>
              <Select id="invite-role" name="role" defaultValue="developer">
                <option value="admin">admin</option>
                <option value="developer">developer</option>
                <option value="operator">operator</option>
                <option value="auditor">auditor</option>
                <option value="member">member</option>
              </Select>
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setInviteOpen(false)}>Cancel</Button>
          <Button type="submit" form="invite-form">Invite</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
