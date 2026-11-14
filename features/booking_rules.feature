Feature: Booking, capacity and cancellation rules of a pickup departure
  Documented rules (use-cases BookCapacity, CancelDeparture; aggregate canvas
  Departure invariants; ADR 0002 A13-A15, A23, A27) that no earlier scenario
  pins: the cutoff boundary, the reason of every BookingFailed, which departure
  a refused booking moves to, the capacity bounds and the cancel reason bounds.

  Background:
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an active sortation center "sc-1"
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And the node "fc-1" is activated
    And the pickup departure "P1" is the 1st from "fc-1"
    And the pickup departure "P2" is the 2nd from "fc-1"
    And the pickup departure "P3" is the 3rd from "fc-1"

  # --- no booking after cutoffAt (ErrDepartureClosed) -------------------------

  Scenario: A lock at exactly the cutoff instant still books the departure
    Given the clock moves to "2026-10-23T21:00:00Z"
    When the assignment "as-1" is locked on "P1"
    Then the command succeeds
    And the assignment "as-1" is booked on "P1"
    And the departure "P1" holds 1 booked package

  Scenario: A lock one second after the cutoff moves to the next pickup
    Given the clock moves to "2026-10-23T21:00:01Z"
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingMoved of "as-1" from "P1" to "P2"
    And the last command published 1 "DepartureCapacityChanged" event
    And the departure "P1" holds 0 booked packages
    And the departure "P2" holds 1 booked package

  Scenario: A late lock with no later pickup fails as DEPARTURE_CLOSED
    Given the capacity of every pickup departure of "fc-1" is changed to 0
    And the clock moves to "2026-10-23T21:00:01Z"
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingFailed of "as-1" on "P1" because "DEPARTURE_CLOSED"
    And the assignment "as-1" is in state "FAILED"

  Scenario: A lock on a pickup whose load is already closed moves to the next pickup
    Given I open the load "LD1" on the departure "P1"
    And the package "TRK-1" is added to the load "LD1"
    And the load "LD1" is closed
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingMoved of "as-1" from "P1" to "P2"
    And the departure "P1" is CLOSED
    And the departure "P1" holds 0 booked packages

  # --- booked <= capacity (ErrDepartureFull) and the reasons of BookingFailed -

  Scenario: A lock on a full pickup with nothing else free fails as DEPARTURE_FULL
    Given the capacity of every pickup departure of "fc-1" is changed to 0
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingFailed of "as-1" on "P1" because "DEPARTURE_FULL"
    And the assignment "as-1" is in state "FAILED"
    And the departure "P1" holds 0 booked packages

  Scenario: A lock on a cancelled pickup moves to the next pickup
    Given the departure "P1" is cancelled because "weather"
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingMoved of "as-1" from "P1" to "P2"
    And the departure "P1" is CANCELLED

  Scenario: A lock on a cancelled pickup with nothing else free fails as DEPARTURE_CANCELLED
    Given the capacity of every pickup departure of "fc-1" is changed to 0
    And the departure "P1" is cancelled because "weather"
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingFailed of "as-1" on "P1" because "DEPARTURE_CANCELLED"

  Scenario: A lock on a pickup that does not exist is booked on the first pickup of the node
    When the assignment "as-1" is locked on the unknown departure "dep-ghost" at node "fc-1"
    Then the last command published BookingMoved of "as-1" from "dep-ghost" to "P1"
    And the assignment "as-1" is booked on "P1"

  Scenario: A lock on a pickup that does not exist fails as DEPARTURE_UNKNOWN when the node has none
    When the assignment "as-1" is locked on the unknown departure "dep-ghost" at node "fc-9"
    Then the last command published BookingFailed of "as-1" on "dep-ghost" because "DEPARTURE_UNKNOWN"
    And the assignment "as-1" is in state "FAILED"

  # --- which departure takes a refused booking (ADR 0002 A14) ----------------

  Scenario: The next pickup skips a departure that is full
    Given the capacity of "P1" is changed to 1
    And the capacity of "P2" is changed to 0
    And the assignment "as-1" is locked on "P1"
    When the assignment "as-2" is locked on "P1"
    Then the last command published BookingMoved of "as-2" from "P1" to "P3"
    And the departure "P3" holds 1 booked package
    And the departure "P2" holds 0 booked packages

  Scenario: A refused booking never moves to an earlier pickup
    Given the capacity of "P2" is changed to 1
    And the assignment "as-1" is locked on "P2"
    When the assignment "as-2" is locked on "P2"
    Then the last command published BookingMoved of "as-2" from "P2" to "P3"
    And the departure "P1" holds 0 booked packages

  Scenario: A refused booking never moves to a pickup whose cutoff has already passed
    Given the clock moves to "2026-10-26T22:00:00Z"
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingMoved of "as-1" from "P1" to "P3"
    And the departure "P2" holds 0 booked packages

  Scenario: A lock on an unknown pickup is never moved onto a line-haul departure of the node
    Given an active delivery station "ds-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    When the assignment "as-1" is locked on the unknown departure "dep-ghost" at node "sc-1"
    Then the last command published BookingFailed of "as-1" on "dep-ghost" because "DEPARTURE_UNKNOWN"

  Scenario: A refused booking never leaves its node for another node's room
    Given a vendor fulfillment center "fc-2" with a pickup cutoff at "18:00" on weekdays
    And an open GROUND lane "ln-2" from "fc-2" to "sc-1"
    And the node "fc-2" is activated
    And the pickup departure "Q1" is the 1st from "fc-2"
    And the capacity of every pickup departure of "fc-1" is changed to 0
    When the assignment "as-1" is locked on "P1"
    Then the last command published BookingFailed of "as-1" on "P1" because "DEPARTURE_FULL"
    And the departure "Q1" holds 0 booked packages

  # --- cancelling a departure rebooks what it holds (ADR 0002 A15) -----------

  Scenario: Cancelling a pickup moves the bookings that fit and fails the rest
    Given the capacity of every pickup departure of "fc-1" is changed to 0
    And the capacity of "P1" is changed to 2
    And the capacity of "P2" is changed to 1
    And the assignment "as-1" is locked on "P1"
    And the assignment "as-2" is locked on "P1"
    When the departure "P1" is cancelled because "breakdown"
    Then the command succeeds
    And the last command published 1 "DepartureCancelled" event
    And the last command published 1 "BookingMoved" event
    And the last command published 1 "BookingFailed" event
    And the departure "P2" holds 1 booked package
    And the departure "P1" holds 0 booked packages
    And no DepartureCapacityChanged was published for "P1"

  Scenario: Suspending the node leaves a booked assignment with no pickup to move to
    Given the assignment "as-1" is locked on "P1"
    When the node "fc-1" is suspended
    Then the last command published BookingFailed of "as-1" on "P1" because "DEPARTURE_CANCELLED"
    And the assignment "as-1" is in state "FAILED"
    And the departure "P1" holds 0 booked packages

  # --- ChangeDepartureCapacity (A23) -----------------------------------------

  Scenario: Capacity equal to the booked packages is allowed and the next lock moves on
    Given the assignment "as-1" is locked on "P1"
    And the assignment "as-2" is locked on "P1"
    When the capacity of "P1" is changed to 2
    Then the command succeeds
    And the last DepartureCapacityChanged of "P1" reports capacity 2, booked 2 and remaining 0
    When the assignment "as-3" is locked on "P1"
    Then the last command published BookingMoved of "as-3" from "P1" to "P2"

  Scenario: Changing a capacity to the value it already has publishes nothing
    When the capacity of "P1" is changed to 40
    Then the command succeeds
    And no event was published by the last command

  Scenario Outline: A capacity inside the bounds is accepted
    When the capacity of "P1" is changed to <capacity>
    Then the command succeeds
    And the departure "P1" has capacity <capacity>

    Examples:
      | capacity   |
      | 0          |
      | 2147483647 |

  Scenario Outline: A capacity outside the bounds is refused
    When the capacity of "P1" is changed to <capacity>
    Then the command is refused with "validation-failed"
    And the departure "P1" has capacity 40

    Examples:
      | capacity   |
      | -1         |
      | 2147483648 |

  Scenario: The capacity of a cancelled departure cannot change
    Given the departure "P1" is cancelled because "weather"
    When the capacity of "P1" is changed to 10
    Then the command is refused with "illegal-transition"

  Scenario: The capacity of a closed departure cannot change
    Given I open the load "LD1" on the departure "P1"
    And the load "LD1" is closed
    When the capacity of "P1" is changed to 10
    Then the command is refused with "illegal-transition"

  Scenario: The capacity of an unknown departure is not found
    When the capacity of "dep-ghost" is changed to 5
    Then the command is refused with "not-found"

  # --- CancelDeparture --------------------------------------------------------

  Scenario: A cancellation is announced with its reason and instant
    When the departure "P1" is cancelled because "weather"
    Then the last command published DepartureCancelled of "P1" with reason "weather" at "2026-10-23T12:00:00Z"
    And the departure "P1" is CANCELLED

  Scenario Outline: A cancel reason of 1 to 200 characters is accepted
    When the departure "P1" is cancelled with a reason of <length> characters
    Then the command succeeds
    And the departure "P1" is CANCELLED

    Examples:
      | length |
      | 1      |
      | 200    |

  Scenario Outline: A cancel reason that is empty or longer than 200 characters is refused
    When the departure "P1" is cancelled with a reason of <length> characters
    Then the command is refused with "validation-failed"
    And the departure "P1" is SCHEDULED

    Examples:
      | length |
      | 0      |
      | 201    |

  Scenario: A cancel reason with a leading blank is refused
    When the departure "P1" is cancelled because " weather"
    Then the command is refused with "validation-failed"
    And the departure "P1" is SCHEDULED
