Feature: Reading departures
  ListDepartures and the keyset-paged GET /departures (ADR 0002 A24): filters
  on origin, destination and cutoff window (from inclusive, to exclusive),
  order by cutoff, limits and paging that never repeats or skips a departure.
  The clock is Friday 2026-10-23 12:00 UTC. fc-1 has ten weekday pickups to
  sc-1; sc-1 has two Friday line-hauls to ds-1.

  Background:
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an active sortation center "sc-1"
    And an active delivery station "ds-1"
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the node "fc-1" is activated
    And departures are generated

  Scenario: Departures are listed by origin
    When I list the departures with origin "sc-1"
    Then the list holds 2 departures

  Scenario: Departures are listed by destination
    When I list the departures with destination "sc-1"
    Then the list holds 10 departures

  Scenario: An origin without departures lists none
    When I list the departures with origin "fc-9"
    Then the command succeeds
    And the list holds 0 departures

  Scenario: The cutoff window includes its start and excludes its end
    When I list the departures with a cutoff from "2026-10-26T21:00:00Z" until "2026-10-28T21:00:00Z"
    Then the list holds 2 departures

  Scenario: A window that starts after it ends is refused
    When I list the departures with a cutoff from "2026-10-28T21:00:00Z" until "2026-10-26T21:00:00Z"
    Then the command is refused with "validation-failed"

  Scenario: A window that starts where it ends lists nothing
    When I list the departures with a cutoff from "2026-10-26T21:00:00Z" until "2026-10-26T21:00:00Z"
    Then the command succeeds
    And the list holds 0 departures

  Scenario: Departures of every origin are listed by cutoff
    When I list the departures with a limit of 1000
    Then the list holds 12 departures
    And the list is ordered by cutoff

  Scenario: A list limit of 1000 is accepted
    When I list the departures with a limit of 1000
    Then the command succeeds

  Scenario Outline: A list limit above 1000 or below zero is refused
    When I list the departures with a limit of <limit>
    Then the command is refused with "validation-failed"

    Examples:
      | limit |
      | 1001  |
      | -1    |

  Scenario: A page limit of 500 is accepted
    When I page the departures with a limit of 500
    Then the command succeeds

  Scenario Outline: A page limit above 500 or below zero is refused
    When I page the departures with a limit of <limit>
    Then the command is refused with "validation-failed"

    Examples:
      | limit |
      | 501   |
      | -1    |

  Scenario: Two pages that exactly fill the list leave no third page
    When I page through the departures of origin "fc-1" 5 at a time
    Then the pages hold "5,5" departures without repeats

  Scenario: A last page may be shorter than the others
    When I page through the departures of origin "fc-1" 4 at a time
    Then the pages hold "4,4,2" departures without repeats

  Scenario: A departure that does not exist is not found
    When I look up the departure "dep-ghost"
    Then the command is refused with "not-found"
