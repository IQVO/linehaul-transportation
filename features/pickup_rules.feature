Feature: Pickup planning rules of a vendor fulfillment center
  Policy PickupFromCutoffs (business context, aggregate canvas, ADR 0002
  A5-A11): one pickup per declared cutoff per operating day of the 14-day
  horizon, sized from the node's throughput cap, toward the node's default
  outbound lane, and re-planned as nodes, profiles and lanes change. The
  clock is Friday 2026-10-23 12:00 UTC, which is 09:00 at the nodes
  (America/Sao_Paulo, UTC-3).

  Background:
    Given an active sortation center "sc-1"

  # --- which days and cutoffs get a pickup ----------------------------------

  Scenario: Every operating day of the horizon gets one pickup and the weekend none
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 10 scheduled pickup departures
    And the earliest scheduled pickup of "fc-3" is cut off at "2026-10-23T21:00:00Z"
    And the lane "ln-3" has a departure cutting off at "2026-11-05T21:00:00Z"
    And the lane "ln-3" has no departure cutting off at "2026-11-06T21:00:00Z"
    And the lane "ln-3" has no departure cutting off at "2026-10-24T21:00:00Z"
    And the lane "ln-3" has no departure cutting off at "2026-10-25T21:00:00Z"

  Scenario: A declared holiday gets no pickup
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and holidays "2026-10-26" and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 9 scheduled pickup departures
    And the lane "ln-3" has no departure cutting off at "2026-10-26T21:00:00Z"
    And the lane "ln-3" has a departure cutting off at "2026-10-27T21:00:00Z"

  Scenario: A cutoff that names weekdays only produces pickups on those days
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days              | maxOrders |
      | 18:00     | tuesday,thursday  | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 4 scheduled pickup departures
    And the lane "ln-3" has a departure cutting off at "2026-10-27T21:00:00Z"
    And the lane "ln-3" has no departure cutting off at "2026-10-28T21:00:00Z"

  Scenario: A cutoff on a day the node is closed produces no pickup
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days     | maxOrders |
      | 18:00     | saturday | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures

  Scenario Outline: A cutoff that is not after the current instant gets no pickup today
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | <cutoff>  |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has <pickups> scheduled pickup departures
    And the earliest scheduled pickup of "fc-3" is cut off at "<first>"

    Examples:
      | cutoff | pickups | first                |
      | 09:00  | 9       | 2026-10-26T12:00:00Z |
      | 09:01  | 10      | 2026-10-23T12:01:00Z |

  Scenario: A cutoff is read in the time zone of its node
    Given a vendor-operated fulfillment center "fc-4" is registered in zone "America/Manaus"
    And "fc-4" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-4" from "fc-4" to "sc-1" taking 120 minutes
    When the node "fc-4" is activated
    Then the earliest scheduled pickup of "fc-4" is cut off at "2026-10-23T22:00:00Z"

  # --- what a pickup announces and how big it is ------------------------------

  Scenario: A pickup is announced with its lane, nodes, times and capacity
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the last command published 10 "DepartureScheduled" events
    And the last command scheduled a pickup on lane "ln-3" from "fc-3" to "sc-1" cutting off at "2026-10-23T21:00:00Z", leaving at "2026-10-23T21:00:00Z", arriving at "2026-10-23T23:00:00Z" with capacity 40

  Scenario Outline: The capacity of a pickup is the orders the node handles in the window before its cutoff
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of <cap> orders per <unit> and the cutoffs:
      | localTime | days | maxOrders |
      | <cutoff>  |      | <max>     |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the departure "Q" is the one on lane "ln-3" cutting off at "<at>"
    And the departure "Q" has capacity <packages>

    Examples:
      | cap | unit | cutoff | max | at                   | packages |
      | 10  | HOUR | 18:00  |     | 2026-10-23T21:00:00Z | 120      |
      | 10  | HOUR | 18:00  | 40  | 2026-10-23T21:00:00Z | 40       |
      | 10  | HOUR | 18:00  | 500 | 2026-10-23T21:00:00Z | 120      |
      | 100 | DAY  | 18:00  |     | 2026-10-23T21:00:00Z | 75       |
      | 10  | HOUR | 06:30  |     | 2026-10-26T09:30:00Z | 10       |
      | 1   | DAY  | 07:00  |     | 2026-10-26T10:00:00Z | 1        |
      | 7   | HOUR | 07:30  |     | 2026-10-26T10:30:00Z | 10       |

  Scenario: The window of a cutoff starts at the previous cutoff of the same day
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 10:00     |      |           |
      | 18:00     |      |           |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the departure "Q1" is the one on lane "ln-3" cutting off at "2026-10-23T13:00:00Z"
    And the departure "Q1" has capacity 40
    And the departure "Q2" is the one on lane "ln-3" cutting off at "2026-10-23T21:00:00Z"
    And the departure "Q2" has capacity 80

  Scenario: The packages per order setting multiplies the capacity of a pickup
    Given the service books 3 packages per order
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the departure "Q" is the one on lane "ln-3" cutting off at "2026-10-23T21:00:00Z"
    And the departure "Q" has capacity 120

  Scenario: The packages per order setting is accepted up to 1000
    Given the service books 1000 packages per order
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the command succeeds
    And the departure "Q" is the one on lane "ln-3" cutting off at "2026-10-23T21:00:00Z"
    And the departure "Q" has capacity 40000

  Scenario: A packages per order setting above 1000 plans no pickup
    Given the service books 1001 packages per order
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the command is refused with "validation-failed"
    And the node "fc-3" has 0 scheduled pickup departures

  Scenario: A daily cap over hours that do not increase is spread over 24 hours
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "06:00" with a cap of 100 orders per DAY and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      |           |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the departure "Q" is the one on lane "ln-3" cutting off at "2026-10-23T21:00:00Z"
    And the departure "Q" has capacity 50

  # --- the default outbound lane (ADR 0002 A8) --------------------------------

  Scenario: A sortation center is preferred over a nearer delivery station
    Given an active delivery station "ds-1"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-a" from "fc-3" to "ds-1" taking 60 minutes
    And an open GROUND lane "ln-b" from "fc-3" to "sc-1" taking 180 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has pickup departures toward "sc-1"

  Scenario: Among sortation centers the shortest transit wins
    Given an active sortation center "sc-2"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-a" from "fc-3" to "sc-1" taking 120 minutes
    And an open GROUND lane "ln-b" from "fc-3" to "sc-2" taking 90 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has pickup departures toward "sc-2"

  Scenario: With equal transit the lowest lane id wins
    Given an active sortation center "sc-2"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-b" from "fc-3" to "sc-1" taking 120 minutes
    And an open GROUND lane "ln-a" from "fc-3" to "sc-2" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has pickup departures toward "sc-2"

  Scenario: A lane whose destination is not known yet ranks after a known one
    Given an active delivery station "ds-1"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-a" from "fc-3" to "sc-ghost" taking 30 minutes
    And an open GROUND lane "ln-b" from "fc-3" to "ds-1" taking 200 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has pickup departures toward "ds-1"

  Scenario: An AIR lane is never the default outbound lane
    Given an active air hub "ah-1"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open AIR lane "ln-a" from "fc-3" to "ah-1" taking 60 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures

  Scenario: A closed lane is not the default outbound lane
    Given an active delivery station "ds-1"
    And a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-a" from "fc-3" to "sc-1" taking 120 minutes
    And an open GROUND lane "ln-b" from "fc-3" to "ds-1" taking 120 minutes
    And the lane "ln-a" is closed
    When the node "fc-3" is activated
    Then the node "fc-3" has pickup departures toward "ds-1"

  # --- whichever input arrives last completes the plan (ADR 0002 A7) ----------

  Scenario: Pickups wait for the outbound lane
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures
    When an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    Then the node "fc-3" has 10 scheduled pickup departures
    And the last command published 10 "DepartureScheduled" events

  Scenario: Pickups wait for the destination node to register
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-7" taking 120 minutes
    And the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures
    When an active sortation center "sc-7"
    Then the node "fc-3" has pickup departures toward "sc-7"

  Scenario: A profile declared after the activation schedules the pickups
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures
    When "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    Then the node "fc-3" has 10 scheduled pickup departures

  Scenario: A fulfillment center run by the network gets no pickups
    Given a network-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    When the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures

  # --- the node leaving and returning to service (ADR 0002 A10, A11) ----------

  Scenario: A reactivated node gets fresh pickups and the cancelled ones stay cancelled
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the node "fc-3" is suspended
    When the node "fc-3" is activated
    Then the node "fc-3" has 10 scheduled pickup departures
    And the node "fc-3" has 10 pickup departures cancelled because "NODE_SUSPENDED"

  Scenario: A retired node stays retired when an activation arrives
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the node "fc-3" is retired
    When the node "fc-3" is activated
    Then the node "fc-3" has 0 scheduled pickup departures
    And the node "fc-3" has 10 pickup departures cancelled because "NODE_RETIRED"

  Scenario: Suspending the node leaves a pickup whose cutoff has passed alone
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the clock moves to "2026-10-23T22:00:00Z"
    When the node "fc-3" is suspended
    Then the node "fc-3" has 9 pickup departures cancelled because "NODE_SUSPENDED"
    And the node "fc-3" has 1 scheduled pickup departure

  Scenario: A second suspension of the same node publishes nothing
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the node "fc-3" is suspended
    When the node "fc-3" is suspended
    Then the command succeeds
    And no event was published by the last command

  Scenario: A profile revision keeps the pickup that already holds a booking
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the pickup departure "P1" is the 1st from "fc-3"
    And the assignment "as-1" is locked on "P1"
    When "fc-3" declares profile revision 2 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 17:00     |      | 40        |
    Then the command succeeds
    And the departure "P1" is SCHEDULED
    And the departure "P1" holds 1 booked package
    And the node "fc-3" has 9 pickup departures cancelled because "PROFILE_REVISED"
    And the node "fc-3" has 11 scheduled pickup departures

  Scenario: A pickup the planner already cancelled is skipped when the node is suspended
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the pickup departure "P1" is the 1st from "fc-3"
    And the departure "P1" is cancelled because "driver strike"
    When the node "fc-3" is suspended
    Then the command succeeds
    And the last command published 9 "DepartureCancelled" events
    And the node "fc-3" has 9 pickup departures cancelled because "NODE_SUSPENDED"
    And the node "fc-3" has 1 pickup departures cancelled because "driver strike"

  Scenario: A pickup the planner cancelled is not rescheduled by the next roll
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the pickup departure "P1" is the 1st from "fc-3"
    And the departure "P1" is cancelled because "driver strike"
    When departures are generated
    Then no departure was scheduled by the last command
    And the departure "P1" is CANCELLED
    And the node "fc-3" has 9 scheduled pickup departures

  Scenario: A pickup whose cutoff has passed is not touched by a profile revision
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the pickup departure "P1" is the 1st from "fc-3"
    And the clock moves to "2026-10-23T22:00:00Z"
    When "fc-3" declares profile revision 2 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 17:00     |      | 40        |
    Then the command succeeds
    And the departure "P1" is SCHEDULED
    And the node "fc-3" has 9 pickup departures cancelled because "PROFILE_REVISED"

  Scenario: The horizon roll adds the new day and keeps the pickups it already has
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the clock moves to "2026-10-26T12:00:00Z"
    When departures are generated
    Then the command succeeds
    And the last command published 1 "DepartureScheduled" event
    And the lane "ln-3" has a departure cutting off at "2026-11-06T21:00:00Z"
    And the lane "ln-3" has 11 PICKUP departures

  # --- the lane closing (ADR 0002 A12) ----------------------------------------

  Scenario: Closing the lane cancels no pickup and bookings on them continue
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the pickup departure "P1" is the 1st from "fc-3"
    And the lane "ln-3" is closed
    When the assignment "as-1" is locked on "P1"
    Then the assignment "as-1" is booked on "P1"
    And the node "fc-3" has 10 scheduled pickup departures

  Scenario: A closed lane gets no new pickups when the horizon rolls
    Given a vendor-operated fulfillment center "fc-3" is registered in zone "America/Sao_Paulo"
    And "fc-3" declares profile revision 1 opening weekdays from "06:00" to "22:00" with a cap of 10 orders per HOUR and the cutoffs:
      | localTime | days | maxOrders |
      | 18:00     |      | 40        |
    And an open GROUND lane "ln-3" from "fc-3" to "sc-1" taking 120 minutes
    And the node "fc-3" is activated
    And the lane "ln-3" is closed
    And the clock moves to "2026-11-06T12:00:00Z"
    When departures are generated
    Then the command succeeds
    And no departure was scheduled by the last command

  # --- what node-topology may tell this context -------------------------------

  Scenario Outline: A node of an unknown type or operator is refused
    When a node "n-1" of type "<type>" run by "<operator>" is registered
    Then the command is refused with "validation-failed"

    Examples:
      | type             | operator |
      | WAREHOUSE        | VENDOR   |
      | SORTATION_CENTER | FLEET    |

  Scenario Outline: A lane with an unknown mode or a non-positive transit is refused
    When a lane "ln-x" from "sc-1" to "sc-2" in mode "<mode>" taking <minutes> minutes is opened
    Then the command is refused with "validation-failed"

    Examples:
      | mode   | minutes |
      | RAIL   | 60      |
      | GROUND | 0       |
      | GROUND | -5      |
