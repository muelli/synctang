// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.fragment.app.FragmentActivity
import com.github.muelli.synctang.ui.SynctangApp
import com.github.muelli.synctang.ui.SynctangTheme

/**
 * The whole app: one screen that is either "pair with a machine" or
 * "unlock the machine you paired with".
 *
 * Deliberately not ported from the reference app this project borrowed
 * its transport from: the quick settings tile, the home screen widget,
 * and the build-a-second-APK-per-machine trick. The plan for this app
 * asks for one Unlock action, and every one of those features is
 * another entry point that has to be got right in front of a key that
 * unlocks a disk.
 */
class MainActivity : FragmentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The gate is an interface, so test mode swaps it rather than
        // threading a flag through the unlock flow: there is then no
        // branch inside the flow that could behave differently in the
        // build people actually use.
        val gate: BiometricGate =
            if (TestMode.noBiometric) OpenGate else BiometricGateImpl(this)
        setContent {
            SynctangTheme {
                SynctangApp(gate)
            }
        }
    }
}
