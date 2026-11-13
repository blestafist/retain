import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  acceptFriendRequest,
  cancelFriendRequest,
  createDeckShare,
  createFriendRequest,
  declineFriendRequest,
  endDeckShare,
  getFriendRequests,
  getFriends,
  getMyDeckShares,
  getSocialActivity,
  getSocialSettings,
  getWeeklyLeaderboard,
  patchSocialSettings,
  removeFriend,
} from '../api/client';
import { decksQueryKey } from '../content/hooks';

export const friendsQueryKey = ['friends'] as const;
export const friendRequestsQueryKey = ['friend-requests'] as const;
export const socialSettingsQueryKey = ['social-settings'] as const;
export const socialActivityQueryKey = ['social-activity'] as const;
export const leaderboardQueryKey = ['social-leaderboard', 'week'] as const;
export const deckSharesQueryKey = ['deck-shares'] as const;

export function useFriends() {
  return useQuery({ queryKey: friendsQueryKey, queryFn: getFriends });
}

export function useFriendRequests() {
  return useQuery({ queryKey: friendRequestsQueryKey, queryFn: getFriendRequests });
}

export function useSocialSettings() {
  return useQuery({ queryKey: socialSettingsQueryKey, queryFn: getSocialSettings });
}

export function useSocialActivity() {
  return useQuery({ queryKey: socialActivityQueryKey, queryFn: getSocialActivity });
}

export function useWeeklyLeaderboard() {
  return useQuery({ queryKey: leaderboardQueryKey, queryFn: getWeeklyLeaderboard });
}

export function useMyDeckShares() {
  return useQuery({ queryKey: deckSharesQueryKey, queryFn: getMyDeckShares });
}

function invalidateFriendData(queryClient: ReturnType<typeof useQueryClient>) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: friendsQueryKey }),
    queryClient.invalidateQueries({ queryKey: friendRequestsQueryKey }),
    queryClient.invalidateQueries({ queryKey: deckSharesQueryKey }),
    queryClient.invalidateQueries({ queryKey: socialActivityQueryKey }),
    queryClient.invalidateQueries({ queryKey: leaderboardQueryKey }),
  ]);
}

export function useCreateFriendRequest() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: createFriendRequest,
    onSuccess: () => invalidateFriendData(queryClient),
  });
}

export function useAcceptFriendRequest() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: acceptFriendRequest,
    onSuccess: () => invalidateFriendData(queryClient),
  });
}

export function useDeclineFriendRequest() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: declineFriendRequest,
    onSuccess: () => invalidateFriendData(queryClient),
  });
}

export function useCancelFriendRequest() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: cancelFriendRequest,
    onSuccess: () => invalidateFriendData(queryClient),
  });
}

export function useRemoveFriend() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: removeFriend,
    onSuccess: async () => {
      await Promise.all([
        invalidateFriendData(queryClient),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}

export function usePatchSocialSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: patchSocialSettings,
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: socialSettingsQueryKey }),
        queryClient.invalidateQueries({ queryKey: socialActivityQueryKey }),
        queryClient.invalidateQueries({ queryKey: leaderboardQueryKey }),
      ]);
    },
  });
}

export function useCreateDeckShare() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ deckId, recipientId, mode }: { deckId: number | string; recipientId: number; mode: 'copy' | 'subscribe' }) =>
      createDeckShare(deckId, recipientId, mode),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: deckSharesQueryKey }),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}

export function useEndDeckShare() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ deckId, shareId }: { deckId: number; shareId: number }) => endDeckShare(deckId, shareId),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: deckSharesQueryKey }),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}
