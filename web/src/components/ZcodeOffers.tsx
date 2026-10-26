import { useRef, useState } from 'react'
import { Alert, Anchor, Box, Button, Divider, Group, Stack, Text, Title } from '@mantine/core'
import { useIsMutating, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { claimZcodeOffer, type ZcodeClaimResult, type ZcodeOffer } from '../api'
import { dashboardQueries, queryKeys } from '../queries'

type Attempt = { result?: ZcodeClaimResult; error?: string; reviewed?: boolean }

export default function ZcodeOffers() {
  const [expanded, setExpanded] = useState(false)
  const [attempts, setAttempts] = useState<Record<string, Attempt>>({})
  const [reviewing, setReviewing] = useState(false)
  const [reviewError, setReviewError] = useState('')
  const inFlight = useRef(false)
  const client = useQueryClient()
  const offers = useQuery({ ...dashboardQueries.zcodeOffers(), enabled: expanded })
  const pending = useIsMutating({ mutationKey: ['zcode-claim'] }) > 0
  const riskBlocked = Object.values(attempts).some((attempt) => attempt.result?.failure_kind === 'risk_blocked')
  const claim = useMutation({
    mutationKey: ['zcode-claim'],
    mutationFn: claimZcodeOffer,
    retry: false,
    onSuccess: (result, planID) => setAttempts((current) => ({ ...current, [planID]: { result } })),
    onError: (error, planID) => setAttempts((current) => ({ ...current, [planID]: { error: error.message } })),
    onSettled: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.zcodeQuota }),
        client.invalidateQueries({ queryKey: queryKeys.zcodeOffers }),
      ])
      inFlight.current = false
    },
  })

  async function reviewStatus() {
    setReviewing(true)
    setReviewError('')
    try {
      await Promise.all([
        offers.refetch({ throwOnError: true }),
        client.fetchQuery({ ...dashboardQueries.zcodeQuota(true), staleTime: 0 }),
      ])
      setAttempts((current) => Object.fromEntries(Object.entries(current).map(([id, attempt]) => [id, { ...attempt, reviewed: true }])))
    } catch {
      setReviewError('Could not refresh both offers and quota. Check your account before making another attempt.')
    } finally {
      setReviewing(false)
    }
  }

  function submit(planID: string) {
    if (inFlight.current || pending || riskBlocked || attempts[planID] || !planID.trim()) return
    inFlight.current = true
    setAttempts((current) => ({ ...current, [planID]: {} }))
    claim.mutate(planID)
  }

  return <Stack gap="sm" style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
    <Divider />
    <Group justify="space-between" gap="xs">
      <Title order={6}>Plan offers</Title>
      {!expanded && <Button variant="light" size="xs" mih={44} onClick={() => setExpanded(true)}>View available offers</Button>}
    </Group>
    {expanded && <>
      <Text size="sm" c="dimmed">Review an offer, then claim it explicitly. If verification is required, complete it in the verification tab and return promptly: the saved proof expires after 40 seconds.</Text>
      <Anchor href="/login/zcode" target="_blank" rel="noopener noreferrer" size="sm">Sign in or complete human verification (opens a new tab)</Anchor>
      <Button variant="light" size="xs" mih={44} loading={reviewing} disabled={pending || offers.isFetching || reviewing} onClick={() => void reviewStatus()} style={{ alignSelf: 'flex-start', maxWidth: '100%', height: 'auto', minHeight: 44 }} styles={{ label: { whiteSpace: 'normal' } }}>Refresh offers and quota</Button>
      {offers.isPending && <Text size="sm" role="status">Loading offers…</Text>}
      {offers.error && <Alert color="yellow" title="Offers unavailable">{offers.error.message}</Alert>}
      {reviewError && <Alert color="yellow" title="Status refresh incomplete">{reviewError}</Alert>}
      {offers.data?.plans.length === 0 && <Text size="sm" c="dimmed">No claimable offers are available for this account.</Text>}
      {offers.data?.plans.map((offer, index) => {
        const attempt = attempts[offer.plan_id]
        const complete = attempt?.result?.ok || attempt?.result?.failure_kind === 'already_claimed'
        return <Box key={`${offer.plan_id}-${index}`} style={{ minWidth: 0 }}>
          {index > 0 && <Divider mb="sm" />}
          <OfferDetails offer={offer} />
          <Button mt="sm" size="xs" mih={44} loading={pending && claim.variables === offer.plan_id} disabled={pending || riskBlocked || reviewing || offers.isFetching || offers.isError || !!attempt || !offer.plan_id?.trim()} onClick={() => submit(offer.plan_id)} aria-label={`Claim ${offer.name || offer.plan_id}`}>
            {complete ? 'Claim confirmed' : 'Claim offer'}
          </Button>
        </Box>
      })}
      {Object.entries(attempts).map(([planID, attempt]) => {
        const complete = attempt.result?.ok || attempt.result?.failure_kind === 'already_claimed'
        const retryable = !complete && ['captcha', 'login_required', 'invalid_request'].includes(attempt.result?.failure_kind ?? '')
        return <Box key={planID}>
          <Text size="xs" c="dimmed">Claim result for {planID}</Text>
          {attempt?.result && <Alert mt="sm" color={complete ? 'teal' : 'yellow'} title={attempt.result.ok ? 'Offer claimed' : attempt.result.failure_kind === 'already_claimed' ? 'Already claimed' : 'Claim was not accepted'} role="status">
            <Text size="sm">{claimResultMessage(attempt.result)}</Text>
            {attempt.result.message && <Text size="sm" mt={4}>{attempt.result.message}</Text>}
          </Alert>}
          {attempt?.error && <Alert mt="sm" color="yellow" title="Claim outcome unknown" role="status">
            <Text size="sm">{attempt.error}</Text>
            <Text size="sm" mt={4}>The offer may have been claimed. No automatic retry was sent. Refresh offers and quota, then check whether the plan activated. Further attempts for this offer are disabled in this session.</Text>
          </Alert>}
          {retryable && !riskBlocked && <>
            <Text size="sm" c="dimmed" mt="sm">Check the refreshed quota and offers before another attempt. If the account status is still unclear, verify it with ZCode first.</Text>
            <Button mt="xs" variant="subtle" size="xs" mih={44} disabled={!attempt.reviewed || pending || reviewing || offers.isError} onClick={() => setAttempts((current) => {
              const next = { ...current }
              delete next[planID]
              return next
            })} style={{ maxWidth: '100%', height: 'auto' }} styles={{ label: { whiteSpace: 'normal' } }}>I checked account status; enable another attempt</Button>
          </>}
        </Box>
      })}
      {Object.keys(attempts).some((id) => !offers.data?.plans.some((offer) => offer.plan_id === id)) && <Text size="sm" role="status">A previously attempted offer is no longer listed. Check the refreshed plan quota to confirm its status.</Text>}
    </>}
  </Stack>
}

function OfferDetails({ offer }: { offer: ZcodeOffer }) {
  return <Stack gap={4}>
    <Text size="sm" fw={600}>{offer.name || offer.plan_id}</Text>
    <Text size="xs" c="dimmed">Offer ID: {offer.plan_id}</Text>
    {offer.description && <Text size="sm">{offer.description}</Text>}
    {offer.entitlement_details.length ? offer.entitlement_details.map((grant, index) => <Text size="xs" c="dimmed" key={`${grant.entitlement_id}-${index}`}>
      {[grant.show_name || grant.entitlement_id, grantLabel(grant.grant_units, grant.unit_type), grant.period].filter(Boolean).join(' · ')}
    </Text>) : <>
      {offer.entitlements.length > 0 && <Text size="xs" c="dimmed">{offer.entitlements.join(' · ')}</Text>}
      {(offer.grant_units != null || offer.period) && <Text size="xs" c="dimmed">{[grantLabel(offer.grant_units, offer.unit_type), offer.period].filter(Boolean).join(' · ')}</Text>}
    </>}
    {offer.starts_at ? <Text size="xs" c="dimmed">Starts {offerDate(offer.starts_at)}</Text> : null}
    {offer.ends_at ? <Text size="xs" c="dimmed">Ends {offerDate(offer.ends_at)}</Text> : null}
  </Stack>
}

function grantLabel(units?: number, unitType?: string) {
  return units != null && Number.isFinite(units) ? `${units.toLocaleString()} ${unitType || 'units'}` : ''
}

function offerDate(value: number) {
  const date = new Date(value > 1e12 ? value : value * 1000)
  return Number.isNaN(date.getTime()) ? 'Not reported' : date.toLocaleString()
}

function claimResultMessage(result: ZcodeClaimResult) {
  if (result.ok) return 'Claim accepted; check refreshed quota to confirm activation.'
  switch (result.failure_kind) {
    case 'already_claimed': return 'This plan was already claimed. Quota and offers are refreshing.'
    case 'captcha': return 'Complete human verification in the verification tab, then return within 40 seconds and refresh status before trying again.'
    case 'login_required': return 'Sign in using the verification tab, then return and refresh status.'
    case 'risk_blocked': return 'ZCode blocked this claim. Resolve the restriction with ZCode. Further attempts are disabled in this session.'
    case 'invalid_request': return 'The offer could not be claimed. Refresh the available offers and check its details.'
    default: return 'The claim could not be confirmed. Check refreshed quota and resolve the outcome with ZCode. Further attempts are disabled in this session.'
  }
}
